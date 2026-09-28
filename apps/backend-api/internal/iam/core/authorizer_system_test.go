package core

import (
	"context"
	"fmt"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestModuleSystemPermissionProviderUsesInjectedService(t *testing.T) {
	h := newRepositoryHarness(t, "sl_")
	seedSystemAccessFixture(t, h.db, "sl_", 11)
	instance := NewModule(h.runtime, enabledModuleConfig())
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, h.repository.ids))
	require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
	require.NoError(t, module.Provide[authz.TokenVerifier](services, httpTokenVerifier{principalID: 11}))
	require.NoError(t, services.Seal())
	permission, err := module.Resolve[PlatformPermissionOn](services)
	require.NoError(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := permission(t.Context(), tx, "account_view_create", 11); return err }), ErrUnavailable)
	require.NoError(t, instance.InjectServices(services))
	bindModuleTables(t, instance, "sl_")
	_, err = h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		allowed, err := permission(t.Context(), tx, "account_view_create", 11)
		require.True(t, allowed)
		return err
	}))
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 11).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		allowed, err := permission(t.Context(), tx, "account_view_create", 11)
		require.False(t, allowed)
		return err
	}))
}

const systemTestRoot guid.ID = 900001

func TestSystemFullAdministratorStandaloneRevocation(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedSystemAccessFixture(t, h.db, "sl_", 11)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	check := func(want bool) {
		t.Helper()
		allowed, err := h.authorizer.CheckPlatform(t.Context(), "account_view_create", 11)
		require.NoError(t, err)
		require.Equal(t, want, allowed)
	}
	check(true)
	require.NoError(t, h.db.Table("sl_iam_principals").Where("id = ?", 11).Update("status", "disabled").Error)
	check(false)
	require.NoError(t, h.db.Table("sl_iam_principals").Where("id = ?", 11).Update("status", "active").Error)
	check(true)
	require.NoError(t, h.db.Table("sl_iam_platform_administrators").Where("principal_id = ?", 11).Delete(&struct{}{}).Error)
	check(false)
}

func seedSystemAccessFixture(t *testing.T, db *gorm.DB, prefix string, principals ...guid.ID) guid.ID {
	t.Helper()
	require.NoError(t, db.Table(prefix+"iam_tenants").Clauses(clause.OnConflict{DoNothing: true}).Create(map[string]any{
		"id": systemTestRoot, "slug": SystemRootSlug, "name": "System", "scope_kind": "system", "status": "active", "version": 1, "created_at": repositoryTestTime, "updated_at": repositoryTestTime,
	}).Error)
	for _, id := range principals {
		require.NoError(t, db.Table(prefix+"iam_principals").Clauses(clause.OnConflict{DoNothing: true}).Create(map[string]any{
			"id": id, "login_name": fmt.Sprintf("system-principal-%d", id), "display_name": fmt.Sprintf("System principal %d", id), "status": "active", "created_at": repositoryTestTime, "updated_at": repositoryTestTime,
		}).Error)
	}
	return systemTestRoot
}

func TestSystemAuthorizationRequiresActiveUnconditionalDirectRole(t *testing.T) {
	h := newCoreServiceHarness(t)
	owner := seedSystemAccessFixture(t, h.db, "sl_", 11)
	_, err := h.repository.PutMember(t.Context(), TenantMember{TenantID: owner, PrincipalID: 11, DisplayName: "Operator", Status: MemberActive})
	require.NoError(t, err)
	role, err := h.repository.CreateRole(t.Context(), owner, "Operator", "")
	require.NoError(t, err)
	_, err = h.repository.GrantPermission(t.Context(), owner, role.ID, "account_view_create")
	require.NoError(t, err)
	check := func(want bool) {
		t.Helper()
		allowed, err := h.authorizer.CheckPlatform(t.Context(), "account_view_create", 11)
		require.NoError(t, err)
		require.Equal(t, want, allowed)
	}
	check(false)
	_, err = h.repository.AddMember(t.Context(), owner, role.ID, 11)
	require.NoError(t, err)
	check(true)
	_, _, err = h.repository.SetPermissionCondition(t.Context(), owner, role.ID, "account_view_create", []byte(`{"principal":{"id":"11"}}`))
	require.NoError(t, err)
	check(false)
	_, _, err = h.repository.SetPermissionCondition(t.Context(), owner, role.ID, "account_view_create", nil)
	require.NoError(t, err)
	check(true)
	require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", owner, 11).Update("status", "disabled").Error)
	check(false)
	require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", owner, 11).Update("status", "active").Error)
	_, err = h.repository.RemoveMember(t.Context(), owner, role.ID, 11)
	require.NoError(t, err)
	check(false)
	// Retired direct grants must never become an authorization fallback.
	require.NoError(t, h.db.Table("sl_iam_platform_grants").Create(map[string]any{"principal_id": 11, "permission_code": "account_view_create", "created_at": repositoryTestTime}).Error)
	check(false)
}

func TestSystemReservedFullRoleCannotCreateFullAuthority(t *testing.T) {
	h := newCoreServiceHarness(t)
	owner := seedSystemAccessFixture(t, h.db, "sl_", 11)
	_, err := h.repository.PutMember(t.Context(), TenantMember{TenantID: owner, PrincipalID: 11, DisplayName: "Operator", Status: MemberActive})
	require.NoError(t, err)
	for _, name := range []string{SystemSuperadministratorRoleName, "Ordinary"} {
		role, err := h.repository.CreateRole(t.Context(), owner, name, "")
		require.NoError(t, err)
		for _, code := range []string{"platform_administer", "account_view_create"} {
			_, err = h.repository.GrantPermission(t.Context(), owner, role.ID, code)
			require.NoError(t, err)
		}
		_, err = h.repository.AddMember(t.Context(), owner, role.ID, 11)
		require.NoError(t, err)
		allowed, err := h.authorizer.CheckPlatform(t.Context(), "platform_administer", 11)
		require.NoError(t, err)
		require.False(t, allowed)
		allowed, err = h.authorizer.CheckPlatform(t.Context(), "account_view_create", 11)
		require.NoError(t, err)
		require.Equal(t, name == "Ordinary", allowed)
	}
}

// A standalone permission read must not become a writer: D55 keeps reads off
// mutation fences, and the system checker once took the global policy fence on
// every restricted administrator's GET. Inside a caller's transaction the fence
// is still required, because mutations rely on it to serialize with revocation.
func TestSystemPermissionReadsWriteOnlyInsideMutationTransactions(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedSystemPermissions(t, h, 11, "system_menu_view")
	writes := 0
	count := func(*gorm.DB) { writes++ }
	require.NoError(t, h.db.Callback().Create().After("gorm:create").Register("probe:count_create", count))
	require.NoError(t, h.db.Callback().Update().After("gorm:update").Register("probe:count_update", count))
	require.NoError(t, h.db.Callback().Delete().After("gorm:delete").Register("probe:count_delete", count))

	for _, code := range []string{"system_menu_view", "system_role_view"} {
		writes = 0
		_, err := h.authorizer.CheckPlatform(t.Context(), code, 11)
		require.NoError(t, err)
		require.Zero(t, writes, "standalone %s check must not write", code)
	}

	writes = 0
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		pinned := *h.authorizer
		pinned.db = tx
		allowed, err := pinned.CheckPlatform(t.Context(), "system_menu_view", 11)
		require.True(t, allowed)
		return err
	}))
	require.NotZero(t, writes, "a check inside a mutation transaction must take the system fence")
}

// The fenced and plain paths must agree on every declared system permission;
// they share one reader, and this keeps a future edit from splitting them.
func TestSystemPermissionFencedAndPlainReadsAgree(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedSystemPermissions(t, h, 11, "system_menu_view", "account_view_create")
	for _, action := range h.service.registry.All() {
		if action.Scope != "platform" {
			continue
		}
		plain, err := h.authorizer.CheckPlatform(t.Context(), action.Code, 11)
		require.NoError(t, err)
		var fenced bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			pinned := *h.authorizer
			pinned.db = tx
			fenced, err = pinned.CheckPlatform(t.Context(), action.Code, 11)
			return err
		}))
		require.Equal(t, fenced, plain, action.Code)
	}
}
