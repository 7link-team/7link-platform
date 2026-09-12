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
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var organizationTestNow = time.Date(2026, 9, 3, 12, 0, 0, 123456789, time.UTC)

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
	return harness
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
	require.NoError(t, db.Exec("CREATE TABLE "+prefix+"iam_policy_revisions (tenant_id BIGINT PRIMARY KEY, revision BIGINT NOT NULL, updated_at DATETIME NOT NULL)").Error)
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
	h := newOrganizationHarness(t, "sl_")
	rootA, err := h.service.Create(t.Context(), 1, CreateDepartment{Name: "Engineering", SortOrder: 10})
	require.NoError(t, err)
	rootB, err := h.service.Create(t.Context(), 1, CreateDepartment{Name: "Operations", SortOrder: 5})
	require.NoError(t, err)
	child, err := h.service.Create(t.Context(), 1, CreateDepartment{ParentID: rootA.ID, Name: "Platform"})
	require.NoError(t, err)
	other, err := h.service.Create(t.Context(), 2, CreateDepartment{Name: "Engineering"})
	require.NoError(t, err)

	items, err := h.service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, []guid.ID{rootB.ID, rootA.ID, child.ID}, []guid.ID{items[0].ID, items[1].ID, items[2].ID})
	assert.Equal(t, []int{0, 0, 1}, []int{items[0].Depth, items[1].Depth, items[2].Depth})
	_, err = h.service.Get(t.Context(), 1, other.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{Name: " engineering "})
	assert.ErrorIs(t, err, ErrNameConflict)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{ParentID: other.ID, Name: "Cross tenant"})
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

func TestDepartmentAuditFailureRollsBackStateAndRevision(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.auditErr = errors.New("audit unavailable")
	_, err := h.service.Create(t.Context(), 1, CreateDepartment{Name: "Engineering"})
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
