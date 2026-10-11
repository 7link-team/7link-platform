package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newPlatformServiceForTest(t *testing.T, h *organizationHarness) (*PlatformService, *tenantBootstrapStub) {
	t.Helper()
	bootstrap, owners := newTenantBootstrapStub(), newTenantOwnerStub()
	service := NewPlatformService(h.runtime, h.appender, newOrganizationTestIDs(500), bootstrap.capability(), owners.capability())
	bindForeignTables(t, service.repo, h.prefix)
	service.now = func() time.Time { return organizationTestNow }
	return service, bootstrap
}

func TestPlatformServiceOwnsIndependentRootsAndExcludesTenants(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	platforms, bootstrap := newPlatformServiceForTest(t, h)
	tenants, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(700))
	first, err := platforms.Create(t.Context(), newTenantForTest("platform-one", "Platform One"))
	require.NoError(t, err)
	second, err := platforms.Create(t.Context(), newTenantForTest("platform-two", "Platform Two"))
	require.NoError(t, err)
	tenant, err := tenants.Create(t.Context(), newTenantForTest("customer-one", "Customer One"))
	require.NoError(t, err)
	require.Equal(t, ScopePlatform, bootstrap.inputs[0].ScopeKind)
	_, err = tenants.Get(t.Context(), first.ID)
	require.ErrorIs(t, err, ErrTenantNotFound)
	_, err = platforms.Get(t.Context(), tenant.ID)
	require.ErrorIs(t, err, ErrTenantNotFound)
	items, total, err := platforms.ListPage(t.Context(), false, "", 0, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 2, total)
	items, err = tenants.List(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, tenant.ID, items[0].ID)
	h.seedMember(t, first.ID, 42, "Member")
	items, err = platforms.ListByMember(t.Context(), 42)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, first.ID, items[0].ID)
	name := "Renamed Platform"
	_, err = platforms.Update(t.Context(), second.ID, UpdatePlatform{Name: &name, Version: second.Version})
	require.NoError(t, err)
	require.ErrorIs(t, tenants.Delete(t.Context(), second.ID, 2), ErrTenantNotFound)
	require.NoError(t, platforms.Delete(t.Context(), second.ID, 2))
}

func TestPlatformCreateRollsBackBootstrapWritesAndSkipsTenantFileRoot(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	platforms, _ := newPlatformServiceForTest(t, h)
	platforms.roots = func(context.Context, *gorm.DB, core.FileRootProvisionInput, time.Time) (guid.ID, error) {
		t.Fatal("platform creation must not provision a tenant file root")
		return 0, nil
	}
	failure := errors.New("bootstrap failure after membership")
	platforms.bootstrap.Bootstrap = func(ctx context.Context, tx *gorm.DB, input core.TenantBootstrapInput, _ time.Time) (core.TenantBootstrapResult, error) {
		var kind string
		require.NoError(t, tx.Table(h.table("iam_tenants")).Select("scope_kind").Where("id = ?", input.TenantID).Scan(&kind).Error)
		require.Equal(t, ScopePlatform, kind)
		require.NoError(t, tx.WithContext(ctx).Table(h.table("iam_tenant_members")).Create(map[string]any{
			"tenant_id": input.TenantID, "principal_id": input.OwnerID, "display_name": "Owner", "email": "", "status": StatusActive,
			"created_at": organizationTestNow, "updated_at": organizationTestNow,
		}).Error)
		return core.TenantBootstrapResult{}, failure
	}
	_, err := platforms.Create(t.Context(), newTenantForTest("rollback-platform", "Rollback Platform"))
	require.ErrorIs(t, err, failure)
	for _, table := range []string{"iam_tenants", "iam_tenant_members", "iam_policy_revisions"} {
		var count int64
		require.NoError(t, h.db.Table(h.table(table)).Count(&count).Error)
		require.Zero(t, count, table)
	}
	platforms.bootstrap.Bootstrap = newTenantBootstrapStub().capability().Bootstrap
	_, err = platforms.Create(t.Context(), newTenantForTest("working-platform", "Working Platform"))
	require.NoError(t, err)
}

func TestPlatformDepartmentsAreEntitylessAndIsolated(t *testing.T) {
	h := newOrganizationDirectoryHarness(t, "sl_")
	platforms, _ := newPlatformServiceForTest(t, h)
	first, err := platforms.Create(t.Context(), newTenantForTest("platform-one", "Platform One"))
	require.NoError(t, err)
	second, err := platforms.Create(t.Context(), newTenantForTest("platform-two", "Platform Two"))
	require.NoError(t, err)
	h.service.roots = func(context.Context, *gorm.DB, core.FileRootProvisionInput, time.Time) (guid.ID, error) {
		t.Fatal("platform departments must not provision tenant file roots")
		return 0, nil
	}
	root, err := h.service.Create(t.Context(), first.ID, CreateDepartment{Name: "Operations"})
	require.NoError(t, err)
	require.Zero(t, root.EntityID)
	child, err := h.service.Create(t.Context(), first.ID, CreateDepartment{Name: "Support", ParentID: root.ID})
	require.NoError(t, err)
	require.Equal(t, 1, child.Depth)
	_, err = h.service.Create(t.Context(), first.ID, CreateDepartment{Name: "Invalid entity", EntityID: organizationEntityOne})
	require.ErrorIs(t, err, ErrEntityMismatch)
	_, err = h.service.Create(t.Context(), second.ID, CreateDepartment{Name: "Invalid parent", ParentID: root.ID})
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{Name: "Tenant needs entity"})
	require.ErrorIs(t, err, ErrInvalid)
	parent := guid.ID(0)
	child, err = h.service.Update(t.Context(), first.ID, child.ID, UpdateDepartment{ParentID: &parent, Version: child.Version})
	require.NoError(t, err)
	require.Zero(t, child.Depth)
	parent = root.ID
	child, err = h.service.Update(t.Context(), first.ID, child.ID, UpdateDepartment{ParentID: &parent, Version: child.Version})
	require.NoError(t, err)
	require.Equal(t, 1, child.Depth)
	require.NoError(t, h.service.Delete(t.Context(), first.ID, child.ID, child.Version))
}
