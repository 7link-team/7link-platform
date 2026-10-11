package organization

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The directory harness intentionally applies only the owner base. Apply the
// real discriminator migration separately from unrelated cross-owner guards.
func scopeFoundationSource(t *testing.T, prefix string) goosex.Source {
	t.Helper()
	const path = "sql/sqlite/00012_administration_scopes.sql"
	data, err := fs.ReadFile((&Module{}).MigrationSource().FS, path)
	require.NoError(t, err)
	return goosex.Source{
		FS:        fstest.MapFS{path: &fstest.MapFile{Data: data}},
		TableName: "goose_organization_scope_fixture", Prefix: prefix,
	}
}

func TestAdministrationScopeMigrationPreservesTenantsAndEnforcesKind(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			db, err := (gormx.Datasource{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "scope.sqlite"), Writable: true}).Open(t.Context())
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			base := (&Module{}).MigrationSource()
			base.Prefix = prefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, base, "sqlite", 7))
			table := prefix + "iam_tenants"
			legacy := map[string]any{"id": 1, "slug": "legacy", "name": "Legacy", "status": TenantSuspended,
				"version": 3, "created_at": organizationTestNow, "updated_at": organizationTestNow}
			require.NoError(t, db.Table(table).Create(legacy).Error)
			source := scopeFoundationSource(t, prefix)
			require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
			var row model.Tenant
			require.NoError(t, db.Table(table).Where("id = 1").Take(&row).Error)
			assert.Equal(t, ScopeTenant, row.ScopeKind)
			assert.Equal(t, "Legacy", row.Name)
			assert.Equal(t, TenantSuspended, row.Status)
			assert.EqualValues(t, 3, row.Version)
			assert.Equal(t, organizationTestNow, row.CreatedAt)
			defaulted := map[string]any{"id": 4, "slug": "defaulted", "name": "Defaulted", "status": TenantActive,
				"version": 1, "created_at": organizationTestNow, "updated_at": organizationTestNow}
			require.NoError(t, db.Table(table).Create(defaulted).Error)
			var defaultRow model.Tenant
			require.NoError(t, db.Table(table).Where("id = 4").Take(&defaultRow).Error)
			assert.Equal(t, ScopeTenant, defaultRow.ScopeKind, "legacy insert callers may omit the discriminator")
			for i, kind := range []string{ScopePlatform, ScopeSystem} {
				root := model.Tenant{ID: guid.ID(i + 2), ScopeKind: kind, Slug: kind, Name: kind,
					Status: TenantActive, Version: 1, CreatedAt: organizationTestNow, UpdatedAt: organizationTestNow}
				require.NoError(t, db.Table(table).Create(&root).Error)
				assert.ErrorContains(t, db.Table(table).Where("id = ?", root.ID).Update("scope_kind", ScopeTenant).Error, "scope kind is immutable")
				require.NoError(t, db.Table(table).Where("id = ?", root.ID).Update("name", "Renamed").Error)
			}
			assert.ErrorContains(t, db.Table(table).Where("id = 1").Update("scope_kind", ScopePlatform).Error, "scope kind is immutable")
			for _, kind := range []string{"unknown", "PLATFORM"} {
				invalid := model.Tenant{ID: 5, ScopeKind: kind, Slug: "invalid", Name: "Invalid", Status: TenantActive,
					Version: 1, CreatedAt: organizationTestNow, UpdatedAt: organizationTestNow}
				assert.Error(t, db.Table(table).Create(&invalid).Error)
			}
			assert.ErrorContains(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 0), "scope kind is immutable")
			row = model.Tenant{}
			require.NoError(t, db.Table(table).Where("id = 2").Take(&row).Error)
			assert.Equal(t, ScopePlatform, row.ScopeKind)
			require.NoError(t, db.Table(table).Where("id IN ?", []int{2, 3}).Delete(&model.Tenant{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 0))
			require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
			row = model.Tenant{}
			require.NoError(t, db.Table(table).Where("id = 1").Take(&row).Error)
			assert.Equal(t, ScopeTenant, row.ScopeKind)
		})
	}
}

func TestOrdinaryTenantOperationsExcludeAdministrativeRoots(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(500))
	tenant, err := service.Create(t.Context(), newTenantForTest("ordinary", "Ordinary"))
	require.NoError(t, err)
	h.seedMember(t, tenant.ID, 42, "Member")
	for i, kind := range []string{ScopePlatform, ScopeSystem} {
		root := model.Tenant{ID: guid.ID(100 + i), ScopeKind: kind, Slug: kind, Name: kind,
			Status: TenantActive, Version: 1, CreatedAt: organizationTestNow, UpdatedAt: organizationTestNow}
		require.NoError(t, h.db.Table(h.table("iam_tenants")).Create(&root).Error)
		h.seedMember(t, root.ID, 42, "Administrator")
		_, err := service.Get(t.Context(), root.ID)
		assert.ErrorIs(t, err, ErrTenantNotFound)
		_, err = service.Update(t.Context(), root.ID, UpdateTenant{Name: stringPointer("Changed"), Version: 1})
		assert.ErrorIs(t, err, ErrTenantNotFound)
		assert.ErrorIs(t, service.Delete(t.Context(), root.ID, 1), ErrTenantNotFound)
		assert.ErrorIs(t, EnsureTenant(t.Context(), h.runtime, root.ID), ErrTenantNotFound)
		_, found, err := service.repo.FindTenantBySlugOn(t.Context(), h.db, kind)
		require.NoError(t, err)
		assert.False(t, found)
		_, err = service.Create(t.Context(), newTenantForTest(kind, "Duplicate"))
		assert.ErrorIs(t, err, ErrTenantSlugConflict, "slug uniqueness remains global across scope kinds")
		var unchanged model.Tenant
		require.NoError(t, h.db.Table(h.table("iam_tenants")).Where("id = ?", root.ID).Take(&unchanged).Error)
		assert.Equal(t, root, unchanged)
	}
	for _, includeDeleted := range []bool{false, true} {
		rows, err := service.List(t.Context(), includeDeleted)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, tenant.ID, rows[0].ID)
		page, total, err := service.ListPage(t.Context(), includeDeleted, "", 0, 1)
		require.NoError(t, err)
		require.Len(t, page, 1)
		assert.EqualValues(t, 1, total)
		assert.Equal(t, tenant.ID, page[0].ID)
	}
	rows, err := service.ListByMember(t.Context(), 42)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, tenant.ID, rows[0].ID)
	require.NoError(t, EnsureTenant(t.Context(), h.runtime, tenant.ID))
	require.NoError(t, EnsureTenant(t.Context(), h.runtime, 600))
	var ensured model.Tenant
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Where("id = 600").Take(&ensured).Error)
	assert.Equal(t, ScopeTenant, ensured.ScopeKind)
}
