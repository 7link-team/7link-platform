package organization

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemRootBootstrapPersistsAndDoesNotClaimTenantOne(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	calls := 0
	m := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(100), appender: h.appender, bootstrap: core.TenantBootstrapper{SeedSystemMenus: func(ctx context.Context, tx *gorm.DB, id guid.ID, _ time.Time) error {
		calls++
		require.NotEqual(t, guid.ID(1), id)
		var root model.Tenant
		require.NoError(t, tx.Table("sl_iam_tenants").Where("id = ?", id).Take(&root).Error)
		require.Equal(t, ScopeSystem, root.ScopeKind)
		return nil
	}}}
	require.NoError(t, m.ensureSystemRoot(t.Context()))
	var root model.Tenant
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("slug = ?", core.SystemRootSlug).Take(&root).Error)
	firstID := root.ID
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("id = ?", root.ID).Update("name", "Edited system").Error)
	require.NoError(t, m.ensureSystemRoot(t.Context()))
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("slug = ?", core.SystemRootSlug).Take(&root).Error)
	require.Equal(t, firstID, root.ID)
	require.Equal(t, "Edited system", root.Name)
	require.Equal(t, 1, calls)
	var original model.Tenant
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("id = 1").Take(&original).Error)
	require.Equal(t, ScopeTenant, original.ScopeKind)
	require.Equal(t, guid.ID(0), h.audits[0].TenantID)
	require.Equal(t, firstID.String(), h.audits[0].Detail["scope_id"])
}

func TestSystemRootBootstrapRollsBackAndRejectsReservedWrongKind(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	want := errors.New("menu seed failed")
	m := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(100), appender: h.appender, bootstrap: core.TenantBootstrapper{SeedSystemMenus: func(context.Context, *gorm.DB, guid.ID, time.Time) error { return want }}}
	require.ErrorIs(t, m.ensureSystemRoot(t.Context()), want)
	var count int64
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("slug = ?", core.SystemRootSlug).Count(&count).Error)
	require.Zero(t, count)
	h.seedTenant(t, 1, TenantActive)
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("id = 1").Update("slug", core.SystemRootSlug).Error)
	require.Error(t, m.ensureSystemRoot(t.Context()))
}

func TestSystemRootBootstrapLosingInsertReadsWinnerWithoutCandidateID(t *testing.T) {
	for _, foundRows := range []bool{false, true} {
		t.Run(fmt.Sprintf("clientFoundRows=%t", foundRows), func(t *testing.T) {
			h := newOrganizationHarness(t, "sl_")
			require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("system_root_race_winner", func(tx *gorm.DB) {
				if tx.Statement.Table != "sl_iam_tenants" {
					return
				}
				err := tx.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (999, 'system', ?, 'Winner', 'active', 1, ?, ?)", core.SystemRootSlug, organizationTestNow, organizationTestNow).Error
				tx.AddError(err)
			}))
			if foundRows {
				require.NoError(t, h.db.Callback().Create().After("gorm:create").Register("system_root_found_rows", func(tx *gorm.DB) {
					if tx.Statement.Table == "sl_iam_tenants" && tx.Error == nil {
						tx.RowsAffected = 1
					}
				}))
			}
			seeds := 0
			m := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(100), appender: h.appender, bootstrap: core.TenantBootstrapper{SeedSystemMenus: func(context.Context, *gorm.DB, guid.ID, time.Time) error { seeds++; return nil }}}
			require.NoError(t, m.ensureSystemRoot(t.Context()))
			require.Zero(t, seeds, "loser must not seed regardless of affected-row reporting")
			require.Empty(t, h.audits)
			var root model.Tenant
			require.NoError(t, h.db.Table("sl_iam_tenants").Where("slug = ?", core.SystemRootSlug).Take(&root).Error)
			require.Equal(t, guid.ID(999), root.ID)
		})
	}
}
