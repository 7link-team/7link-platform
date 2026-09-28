package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var repositoryTestTime = time.Date(2026, 9, 2, 12, 0, 0, 123_000_000, time.UTC)

type repositoryTestIDs struct{ next atomic.Int64 }

func newRepositoryTestIDs(start int64) *repositoryTestIDs {
	ids := &repositoryTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *repositoryTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type repositoryHarness struct {
	repository *Repository
	db         *gorm.DB
	runtime    *appenv.Runtime
	now        *time.Time
}

func newRepositoryHarness(t *testing.T, prefix string) repositoryHarness {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=10000&_journal_mode=WAL",
		filepath.ToSlash(filepath.Join(t.TempDir(), "iam-repository.sqlite")),
	)
	initializeRepositoryDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(
		appenv.NewIdentity("iam-test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	now := repositoryTestTime
	repository := NewRepository(runtime, newRepositoryTestIDs(1000))
	repository.now = func() time.Time { return now }
	repository.foreign = joinedTestTables(t, prefix)
	contributeFileMenu(t, repository.systemMenus)
	return repositoryHarness{repository: repository, db: router.Write(), runtime: runtime, now: &now}
}

func initializeRepositoryDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sources := []goosex.Source{
		{FS: os.DirFS(filepath.Join(filepath.Dir(filename), "..", "identity")), TableName: "goose_identity"},
		(&Module{}).MigrationSource(),
		organizationTestMigrationSource(t),
	}
	for _, source := range sources {
		source.Prefix = prefix
		require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	}
}

func organizationTestMigrationSource(t *testing.T) goosex.Source {
	t.Helper()
	// Importing the organization root here would cycle through its core capabilities.
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	organizationRoot := filepath.Join(filepath.Dir(filename), "..", "organization")
	return goosex.Source{FS: os.DirFS(organizationRoot), TableName: "goose_organization"}
}

func seedTenantAndPrincipal(t *testing.T, db *gorm.DB, prefix string, tenantID, principalID guid.ID) {
	t.Helper()
	now := repositoryTestTime
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (?, ?, ?, 'active', 1, ?, ?)",
		tenantID, fmt.Sprintf("tenant-%d", tenantID), fmt.Sprintf("Tenant %d", tenantID), now, now,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)",
		principalID, fmt.Sprintf("principal-%d", principalID), fmt.Sprintf("Principal %d", principalID), now, now,
	).Error)
}

func TestRepositoryRoleIsolationAndNormalizedNameConflict(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository

	tenantA, tenantB := guid.ID(1), guid.ID(2)
	roleA, err := repository.CreateRole(t.Context(), tenantA, "Admin", "A")
	require.NoError(t, err)
	roleB, err := repository.CreateRole(t.Context(), tenantB, "Admin", "B")
	require.NoError(t, err)

	_, err = repository.CreateRole(t.Context(), tenantA, "  ADMIN  ", "duplicate")
	assert.ErrorIs(t, err, ErrRoleNameConflict)
	var nameKey string
	require.NoError(t, harness.db.Table("sl_iam_roles").Select("name_key").
		Where("tenant_id = ? AND id = ?", tenantA, roleA.ID).Scan(&nameKey).Error)
	assert.Equal(t, "admin", nameKey)

	_, err = repository.GetRole(t.Context(), tenantA, roleB.ID)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	_, err = repository.UpdateRole(t.Context(), tenantA, roleB.ID, "Taken", "cross tenant")
	assert.ErrorIs(t, err, ErrRoleNotFound)
	assert.ErrorIs(t, repository.DeleteRole(t.Context(), tenantA, roleB.ID), ErrRoleNotFound)

	unchanged, err := repository.GetRole(t.Context(), tenantB, roleB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Admin", unchanged.Name)
}

func TestRoleUpdateAssignsItsExplicitTimestampOnce(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	role, err := h.repository.CreateRole(t.Context(), 1, "Before", "old")
	require.NoError(t, err)
	var statements []string
	require.NoError(t, h.db.Callback().Update().After("gorm:update").Register("capture_role_update", func(db *gorm.DB) {
		if db.Statement.Table == "sl_iam_roles" {
			statements = append(statements, db.Statement.SQL.String())
		}
	}))
	t.Cleanup(func() { require.NoError(t, h.db.Callback().Update().Remove("capture_role_update")) })
	updated, err := h.repository.UpdateRole(t.Context(), 1, role.ID, "After", "new")
	require.NoError(t, err)
	assert.Equal(t, "After", updated.Name)
	require.Len(t, statements, 1)
	assert.Equal(t, 1, strings.Count(statements[0], "updated_at"), "PostgreSQL rejects duplicate SET columns even when SQLite accepts them")
	var stored struct{ UpdatedAt time.Time }
	require.NoError(t, h.db.Table("sl_iam_roles").Select("updated_at").Where("id = ?", role.ID).Take(&stored).Error)
	assert.True(t, h.now.Equal(stored.UpdatedAt), "GORM must not replace the application timestamp with its own clock")
}

func TestRepositoryPutMemberPreservesCreatedAtAndNullableDisabledAt(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	tenantID, principalID := guid.ID(1), guid.ID(11)
	seedTenantAndPrincipal(t, harness.db, "sl_", tenantID, principalID)

	created, err := harness.repository.PutMember(t.Context(), TenantMember{
		TenantID: tenantID, PrincipalID: principalID,
		DisplayName: "First", Email: "first@example.test", Status: MemberActive,
	})
	require.NoError(t, err)
	assert.Equal(t, repositoryTestTime, created.CreatedAt)
	assert.True(t, created.DisabledAt.IsZero())

	secondTime := repositoryTestTime.Add(time.Hour)
	*harness.now = secondTime
	disabled, err := harness.repository.PutMember(t.Context(), TenantMember{
		TenantID: tenantID, PrincipalID: principalID,
		DisplayName: "Updated", Email: "updated@example.test", Status: MemberDisabled,
	})
	require.NoError(t, err)
	assert.Equal(t, repositoryTestTime, disabled.CreatedAt, "upsert must preserve created_at")
	assert.Equal(t, secondTime, disabled.UpdatedAt)
	assert.Equal(t, secondTime, disabled.DisabledAt)

	thirdTime := secondTime.Add(time.Hour)
	*harness.now = thirdTime
	active, err := harness.repository.SetMemberStatus(t.Context(), tenantID, principalID, MemberActive)
	require.NoError(t, err)
	assert.Equal(t, repositoryTestTime, active.CreatedAt)
	assert.True(t, active.DisabledAt.IsZero(), "reactivation must store disabled_at as NULL")
}

func TestRepositoryMenuIsolationAndNullableUpdates(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	parent, err := repository.CreateMenu(t.Context(), Menu{
		TenantID: 1, Label: "Parent", Route: "/parent", Status: MenuActive,
	})
	require.NoError(t, err)
	child, err := repository.CreateMenu(t.Context(), Menu{
		TenantID: 1, ParentID: parent.ID, Label: "Child", Route: "/child", Status: MenuActive,
	})
	require.NoError(t, err)
	other, err := repository.CreateMenu(t.Context(), Menu{
		TenantID: 2, Label: "Other", Route: "/other", Status: MenuActive,
	})
	require.NoError(t, err)

	_, err = repository.GetMenu(t.Context(), 1, other.ID)
	assert.ErrorIs(t, err, ErrMenuNotFound)
	other.TenantID, other.Label = 1, "Cross tenant"
	_, err = repository.UpdateMenu(t.Context(), other)
	assert.ErrorIs(t, err, ErrMenuNotFound)
	assert.ErrorIs(t, repository.DeleteMenu(t.Context(), 1, other.ID), ErrMenuNotFound)

	child.ParentID, child.Route = 0, ""
	updated, err := repository.UpdateMenu(t.Context(), child)
	require.NoError(t, err)
	assert.Zero(t, updated.ParentID)
	assert.Empty(t, updated.Route)
	var nulls struct {
		ParentNull int `gorm:"column:parent_null"`
		RouteNull  int `gorm:"column:route_null"`
	}
	require.NoError(t, harness.db.Raw(
		"SELECT parent_id IS NULL AS parent_null, route IS NULL AS route_null FROM sl_iam_menus WHERE tenant_id = ? AND id = ?",
		child.TenantID, child.ID,
	).Scan(&nulls).Error)
	assert.Equal(t, 1, nulls.ParentNull)
	assert.Equal(t, 1, nulls.RouteNull)
}

func TestRepositoryPaginationAppliesLimitAndKeepsTenantTotals(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	repository := harness.repository
	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		_, err := repository.CreateRole(t.Context(), 1, name, "")
		require.NoError(t, err)
	}
	_, err := repository.CreateRole(t.Context(), 2, "Other", "")
	require.NoError(t, err)
	roles, total, err := repository.ListRolesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, roles, 1)
	assert.Equal(t, "Bravo", roles[0].Name)

	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		_, err := repository.CreateEntity(t.Context(), Entity{
			TenantID: 1, Type: "store", Name: name, Status: EntityActive,
		})
		require.NoError(t, err)
	}
	entities, total, err := repository.ListEntitiesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, entities, 1)
	assert.Equal(t, "Bravo", entities[0].Name)
}

// TestRepositoryBindForeignTablesIsRequiredBeforeJoiningThem covers the entry
// point a caller that builds a repository outside the module has to use. Until
// the owners' names arrive, a statement that JOINs them would run against an
// empty table name, so the reader reports the names as missing instead.
func TestRepositoryBindForeignTablesIsRequiredBeforeJoiningThem(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	unbound := NewRepository(harness.runtime, newRepositoryTestIDs(7000))
	role, err := harness.repository.CreateRole(t.Context(), guid.ID(1), "Reviewers", "")
	require.NoError(t, err)

	_, err = unbound.ListDirectoryBindings(t.Context(), 1, role.ID)
	require.ErrorIs(t, err, ErrUnavailable, "an unbound repository must not join an empty table name")
	_, err = unbound.GetRoleDataScope(t.Context(), 1, role.ID)
	require.ErrorIs(t, err, ErrUnavailable)

	// A receiver that does not exist cannot record anything.
	require.ErrorIs(t, (*Repository)(nil).BindForeignTables(
		publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_"),
	), ErrUnavailable)

	// A published set with a gap in it is refused: the JOIN it would build
	// reads a table this deployment does not have.
	partial := publishedDirectoryTables("sl_")
	partial.Departments = gormx.TableName{}
	require.ErrorContains(t, unbound.BindForeignTables(partial, publishedPrincipalTable("sl_")),
		"organization left Departments empty")
	_, err = unbound.ListDirectoryBindings(t.Context(), 1, role.ID)
	require.ErrorIs(t, err, ErrUnavailable, "a refused directory set must not be recorded")
	require.ErrorContains(t, unbound.BindForeignTables(publishedDirectoryTables("sl_"), PrincipalTableNames{}),
		"identity left Principals empty")

	require.NoError(t, unbound.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	bindings, err := unbound.ListDirectoryBindings(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Empty(t, bindings)
	scope, err := unbound.GetRoleDataScope(t.Context(), 1, role.ID)
	require.NoError(t, err)
	require.Equal(t, DataScopeAll, scope.Scope, "a role with no recorded scope defaults to the module default")
}
