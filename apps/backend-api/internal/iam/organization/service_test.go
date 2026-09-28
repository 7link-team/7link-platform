package organization

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var organizationTestNow = time.Date(2026, 9, 3, 12, 0, 0, 123456789, time.UTC)

const (
	organizationEntityOne guid.ID = 10
	organizationEntityTwo guid.ID = 20
)

type organizationTestIDs struct{ next atomic.Int64 }

func newOrganizationTestIDs(start int64) *organizationTestIDs {
	ids := &organizationTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *organizationTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type organizationHarness struct {
	service  *Service
	db       *gorm.DB
	runtime  *appenv.Runtime
	appender audit.Appender
	prefix   string
	auditMu  sync.Mutex
	audits   []audit.DomainEvent
	auditDBs []*gorm.DB
	auditErr error
}

func newOrganizationHarness(t *testing.T, prefix string) *organizationHarness {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on", filepath.ToSlash(filepath.Join(t.TempDir(), "organization.sqlite")))
	initializeOrganizationDatabase(t, dsn, prefix)
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	harness := &organizationHarness{db: router.Write(), prefix: prefix}
	runtime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	appender := audit.Appender(func(_ context.Context, db *gorm.DB, event audit.DomainEvent) error {
		harness.auditMu.Lock()
		defer harness.auditMu.Unlock()
		harness.auditDBs = append(harness.auditDBs, db)
		harness.audits = append(harness.audits, event)
		return harness.auditErr
	})
	harness.runtime, harness.appender = runtime, appender
	harness.service = newBoundService(t, runtime, appender, newOrganizationTestIDs(100))
	harness.service.now = func() time.Time { return organizationTestNow }
	harness.service.administrators = &grantingAdministratorGuard{}
	return harness
}

func newOrganizationDirectoryHarness(t *testing.T, prefix string) *organizationHarness {
	t.Helper()
	h := newOrganizationHarness(t, prefix)
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantActive)
	return h
}

func TestCreateDepartmentProvisionsOrganizationOwnedFileRootInTransaction(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	called := 0
	h.service.roots = func(ctx context.Context, tx *gorm.DB, input core.FileRootProvisionInput, _ time.Time) (guid.ID, error) {
		called++
		require.Equal(t, core.FileRootProvisionInput{
			Scope: "department", TenantID: 1, EntityID: organizationEntityOne, OwnerID: input.OwnerID,
		}, input)
		var count int64
		require.NoError(t, tx.WithContext(ctx).Table("sl_iam_departments").Where("tenant_id = ? AND id = ? AND entity_id = ?", input.TenantID, input.OwnerID, input.EntityID).Count(&count).Error)
		require.Equal(t, int64(1), count, "the provisioner must see the uncommitted department")
		return 9001, nil
	}
	created, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Equal(t, 1, called)
}

func TestCreateDepartmentRollsBackWhenFileRootProvisioningFails(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	want := errors.New("root unavailable")
	h.service.roots = func(context.Context, *gorm.DB, core.FileRootProvisionInput, time.Time) (guid.ID, error) {
		return 0, want
	}
	_, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.ErrorIs(t, err, want)
	departments, err := h.service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Empty(t, departments)
}

func TestDeleteDepartmentRefusesRetainedFileContent(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	created, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	called := 0
	h.service.rootContent = func(ctx context.Context, tx *gorm.DB, input core.FileRootProvisionInput) (bool, error) {
		called++
		require.Equal(t, core.FileRootProvisionInput{
			Scope: "department", TenantID: 1, EntityID: organizationEntityOne, OwnerID: created.ID,
		}, input)
		var count int64
		require.NoError(t, tx.WithContext(ctx).Table("sl_iam_departments").Where("tenant_id = ? AND id = ?", 1, created.ID).Count(&count).Error)
		require.Equal(t, int64(1), count, "the guard must run before department deletion")
		return true, nil
	}
	require.ErrorIs(t, h.service.Delete(t.Context(), 1, created.ID, created.Version), ErrInUse)
	stored, err := h.service.Get(t.Context(), 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, stored.ID)
	require.Equal(t, 1, called)
}

func initializeOrganizationDatabase(t *testing.T, dsn, prefix string) {
	t.Helper()
	db, err := (gormx.Datasource{Type: "sqlite", DSN: dsn, Writable: true}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, "sqlite", 7))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, scopeFoundationSource(t, prefix), "sqlite"))
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_policy_revisions (tenant_id BIGINT PRIMARY KEY, revision BIGINT NOT NULL, updated_at DATETIME NOT NULL)").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_entities (tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, status TEXT NOT NULL, PRIMARY KEY (tenant_id, id))").Error)
	require.NoError(t, db.Exec("ALTER TABLE "+prefix+"iam_departments ADD COLUMN entity_id BIGINT NULL").Error)
	require.NoError(t, db.Exec("INSERT INTO "+prefix+"iam_entities (tenant_id, id, status) VALUES (1, 10, 'active'), (1, 11, 'active'), (2, 20, 'active')").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_tenant_members (tenant_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, display_name TEXT NOT NULL, email TEXT NOT NULL, status TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, PRIMARY KEY (tenant_id, principal_id))").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_relationships (tenant_id BIGINT NOT NULL, subject_type TEXT NOT NULL, subject_id BIGINT NOT NULL)").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_resource_relationships (tenant_id BIGINT NOT NULL, subject_type TEXT NOT NULL, subject_id BIGINT NOT NULL)").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_roles (tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, name TEXT NOT NULL, PRIMARY KEY (tenant_id, id))").Error)
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_role_members (tenant_id BIGINT NOT NULL, role_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, created_at DATETIME NOT NULL, PRIMARY KEY (tenant_id, role_id, principal_id))").Error)
	require.NoError(t, sqlDB.Close())
}

func (h *organizationHarness) table(name string) string { return h.prefix + name }

func (h *organizationHarness) revision(t *testing.T, tenantID guid.ID) int64 {
	t.Helper()
	var revision int64
	err := h.db.Table(h.table("iam_policy_revisions")).Select("revision").Where("tenant_id = ?", tenantID).Scan(&revision).Error
	require.NoError(t, err)
	return revision
}

func TestDepartmentLifecycleAndTenantIsolation(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	rootA, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering", SortOrder: 10})
	require.NoError(t, err)
	rootB, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Operations", SortOrder: 5})
	require.NoError(t, err)
	child, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, ParentID: rootA.ID, Name: "Platform"})
	require.NoError(t, err)
	other, err := h.service.Create(t.Context(), 2, CreateDepartment{EntityID: organizationEntityTwo, Name: "Engineering"})
	require.NoError(t, err)

	items, err := h.service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, []guid.ID{rootB.ID, rootA.ID, child.ID}, []guid.ID{items[0].ID, items[1].ID, items[2].ID})
	assert.Equal(t, []int{0, 0, 1}, []int{items[0].Depth, items[1].Depth, items[2].Depth})
	_, err = h.service.Get(t.Context(), 1, other.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: " engineering "})
	assert.ErrorIs(t, err, ErrNameConflict)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, ParentID: other.ID, Name: "Cross tenant"})
	assert.ErrorIs(t, err, ErrNotFound)

	newParent := rootB.ID
	child, err = h.service.Update(t.Context(), 1, child.ID, UpdateDepartment{ParentID: &newParent, Version: child.Version})
	require.NoError(t, err)
	assert.Equal(t, rootB.ID, child.ParentID)
	cycleParent := child.ID
	_, err = h.service.Update(t.Context(), 1, rootB.ID, UpdateDepartment{ParentID: &cycleParent, Version: rootB.Version})
	assert.ErrorIs(t, err, ErrHierarchyCycle)
	_, err = h.service.Update(t.Context(), 1, child.ID, UpdateDepartment{Name: stringPointer("Renamed"), Version: 1})
	assert.ErrorIs(t, err, ErrVersionConflict)
	assert.ErrorIs(t, h.service.Delete(t.Context(), 1, rootB.ID, rootB.Version), ErrHasChildren)
	require.NoError(t, h.service.Delete(t.Context(), 1, child.ID, child.Version))
	require.NoError(t, h.service.Delete(t.Context(), 1, rootB.ID, rootB.Version))
	assert.EqualValues(t, 6, h.revision(t, 1))
	assert.EqualValues(t, 1, h.revision(t, 2))
	require.Len(t, h.audits, 7)
	for _, db := range h.auditDBs {
		assert.NotSame(t, h.db, db, "audit must receive the active transaction")
	}
}

func TestDepartmentEntityBoundary(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "")
	root, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	other, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: 11, Name: "Operations"})
	require.NoError(t, err)

	_, err = h.service.Create(t.Context(), 1, CreateDepartment{EntityID: 11, ParentID: root.ID, Name: "Cross entity"})
	assert.ErrorIs(t, err, ErrEntityMismatch)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{EntityID: 999, Name: "Missing entity"})
	assert.ErrorIs(t, err, ErrNotFound)

	parent := other.ID
	_, err = h.service.Update(t.Context(), 1, root.ID, UpdateDepartment{ParentID: &parent, Version: root.Version})
	assert.ErrorIs(t, err, ErrEntityMismatch)
	assert.EqualValues(t, 2, h.revision(t, 1))
}

func TestDepartmentAuditFailureRollsBackStateAndRevision(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "")
	h.auditErr = errors.New("audit unavailable")
	_, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	assert.ErrorContains(t, err, "audit unavailable")
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_departments")).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, h.db.Table(h.table("iam_department_closure")).Count(&count).Error)
	assert.Zero(t, count)
	assert.Zero(t, h.revision(t, 1))
}

func TestDepartmentRepositoryFailureAndValidation(t *testing.T) {
	h := newOrganizationHarness(t, "")
	assert.Panics(t, func() {
		NewService(nil, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }, newOrganizationTestIDs(1))
	})
	assert.ErrorIs(t, h.service.Delete(t.Context(), 0, 1, 1), ErrInvalid)
	assert.ErrorIs(t, h.service.Delete(t.Context(), 1, 0, 1), ErrInvalid)
	assert.ErrorIs(t, h.service.Delete(t.Context(), 1, 1, 0), ErrInvalid)
	require.NoError(t, h.db.Exec("DROP TABLE iam_departments").Error)
	_, err := h.service.List(t.Context(), 1)
	assert.Error(t, err)
}

// TestDepartmentWritesAbortWhenThePolicyRevisionCannotAdvance pins that a
// department write is only committed once the tenant's authorization policy
// revision has moved with it. The revision is what tells every cache that its
// view of the directory is stale, so a write that committed without it would
// leave the deployment authorizing against a hierarchy that no longer exists.
func TestDepartmentWritesAbortWhenThePolicyRevisionCannotAdvance(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	created, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_policy_revisions").Error)

	_, err = h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Operations"})
	assert.Error(t, err)
	_, err = h.service.Update(t.Context(), 1, created.ID, UpdateDepartment{
		Name: stringPointer("Platform"), Version: created.Version,
	})
	assert.Error(t, err)
	assert.Error(t, h.service.Delete(t.Context(), 1, created.ID, created.Version))

	items, err := h.service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 1, "no department may be added or removed without a revision bump")
	assert.Equal(t, "Engineering", items[0].Name, "a rejected rename must not survive")
	assert.Equal(t, created.Version, items[0].Version)
}

// TestDepartmentUpdateAppliesStatusAndOrderAndRefusesAMissingParent pins the
// two fields the department update path carries besides the name, and the
// guard that keeps the hierarchy from being re-parented onto a department
// that is not there.
func TestDepartmentUpdateAppliesStatusAndOrderAndRefusesAMissingParent(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	created, err := h.service.Create(t.Context(), 1, CreateDepartment{
		EntityID: organizationEntityOne, Name: "Engineering", SortOrder: 3,
	})
	require.NoError(t, err)

	updated, err := h.service.Update(t.Context(), 1, created.ID, UpdateDepartment{
		Status: stringPointer(StatusDisabled), SortOrder: intPointer(9), Version: created.Version,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, updated.Status)
	assert.Equal(t, 9, updated.SortOrder)
	assert.Equal(t, created.Version+1, updated.Version)
	stored, err := h.service.Get(t.Context(), 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, stored.Status)
	assert.Equal(t, 9, stored.SortOrder)

	missing := guid.ID(987654)
	_, err = h.service.Update(t.Context(), 1, created.ID, UpdateDepartment{
		ParentID: &missing, Version: updated.Version,
	})
	assert.ErrorIs(t, err, ErrNotFound)
}

// A re-parent widens department-and-descendants reach above the new parent,
// so it asks core whether the actor may restructure the tree; a refusal leaves
// the department where it was, and a rename does not ask at all.
func TestDepartmentMoveAsksForGrantAuthority(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	guard := &grantingAdministratorGuard{}
	h.service.administrators = guard
	parent, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Sales"})
	require.NoError(t, err)
	moved, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Support"})
	require.NoError(t, err)
	actor := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 5, TenantID: 1})

	renamed, err := h.service.Update(actor, 1, moved.ID, UpdateDepartment{Name: stringPointer("Customer support"), Version: moved.Version})
	require.NoError(t, err)
	assert.Empty(t, guard.moved, "a rename is not a move")

	guard.moveErr = core.ErrPrivilegeEscalation
	_, err = h.service.Update(actor, 1, moved.ID, UpdateDepartment{ParentID: &parent.ID, Version: renamed.Version})
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
	assert.Equal(t, []guid.ID{1}, guard.moved)
	stored, err := h.service.Get(t.Context(), 1, moved.ID)
	require.NoError(t, err)
	assert.Zero(t, stored.ParentID, "a refused move changes nothing")

	guard.moveErr = nil
	_, err = h.service.Update(actor, 1, moved.ID, UpdateDepartment{ParentID: &parent.ID, Version: renamed.Version})
	require.NoError(t, err)
	stored, err = h.service.Get(t.Context(), 1, moved.ID)
	require.NoError(t, err)
	assert.Equal(t, parent.ID, stored.ParentID)

	h.service.administrators = &organizationAdministratorGuard{}
	root := guid.ID(0)
	_, err = h.service.Update(actor, 1, moved.ID, UpdateDepartment{ParentID: &root, Version: renamed.Version + 1})
	assert.ErrorIs(t, err, ErrUnavailable, "a guard without the move check fails closed")
}

// TestDepartmentServiceRejectsInvalidInputAndAnUnwiredRepository pins the
// guards that run before the department service reaches storage. An unwired
// service must report that it is unavailable rather than dereference a nil
// writer, and a malformed selector must be refused rather than widened into a
// cross-tenant read.
func TestDepartmentServiceRejectsInvalidInputAndAnUnwiredRepository(t *testing.T) {
	unwired := &Service{now: func() time.Time { return organizationTestNow }}
	_, err := unwired.List(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = unwired.Get(t.Context(), 1, 2)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	h := newOrganizationHarness(t, "sl_")
	_, err = h.service.Get(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.Create(t.Context(), 0, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.Update(t.Context(), 1, 0, UpdateDepartment{Version: 1})
	assert.ErrorIs(t, err, ErrInvalid)

	failing := newBoundService(t, h.runtime, h.appender, failingOrganizationIDs{})
	_, err = failing.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: "Engineering"})
	assert.ErrorContains(t, err, "generate department id")
}
