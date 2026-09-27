package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountViewAdmissionUsesLiveOperatorAndExactTargetOwner(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	h.seedAdditionalMember(t, 2, 33, MemberActive)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 33)
	require.NoError(t, err)
	members := NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	m := &Module{runtime: h.runtime, checker: h.authorizer, members: members, registrar: authz.NewDeclarationOnlyRegistrar(h.service.registry)}
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 2, Name: "Entity", Type: "unit", Status: EntityActive})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, scope string
		target, id  guid.ID
		allowed     bool
	}{
		{"personal", "personal", 22, 22, true},
		{"personal-other-id", "personal", 22, 11, false},
		{"own-platform", "platform", 11, 1, true},
		{"tenant-member-not-platform", "platform", 22, 1, false},
		{"own-tenant", "tenant", 22, 2, true},
		{"platform-root-not-tenant", "tenant", 11, 1, false},
		{"platform-member-not-tenant", "tenant", 11, 2, false},
		{"own-entity", "entity", 22, entity.ID, true},
		{"other-entity-member", "entity", 11, entity.ID, false},
		{"ordinary-system", "system", 22, 1, false},
		{"administrator-system", "system", 33, 1, true},
		{"system-wrong-id", "system", 33, 2, false},
		{"organization-platform", "platform", 0, 1, true},
		{"organization-tenant", "tenant", 0, 2, true},
		{"organization-wrong-kind", "tenant", 0, 1, false},
		{"organization-not-a-personal-view", "personal", 0, 33, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return m.authorizeAccountView(t.Context(), tx, 33, tc.target, tc.scope, tc.id)
			})
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			}
		})
	}
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 33).Error)
	err = h.db.Transaction(func(tx *gorm.DB) error { return m.authorizeAccountView(t.Context(), tx, 33, 22, "personal", 22) })
	require.ErrorIs(t, err, authnext.ErrInvalidCredentials, "removed operator authority must be effective on next use")
	require.ErrorIs(t, (&Module{}).authorizeAccountView(t.Context(), h.db, 33, 22, "system", 1), authnext.ErrInvalidCredentials)
}

func TestAccountViewWorkspaceFollowsVerifiedScope(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 2, Name: "Selected entity", Type: "unit", Status: EntityActive})
	require.NoError(t, err)
	workspace, err := h.service.accountViewWorkspace(t.Context(), "entity", entity.ID)
	require.NoError(t, err)
	require.Equal(t, entity.ID, workspace.Entity.ID)
	require.EqualValues(t, 2, workspace.Tenant.ID)
	require.Nil(t, workspace.OperatingPlatform)
	workspace, err = h.service.accountViewWorkspace(t.Context(), "platform", 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, workspace.OperatingPlatform.ID)
	require.Nil(t, workspace.Tenant)
	_, err = h.service.accountViewWorkspace(t.Context(), "tenant", 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = h.service.accountViewWorkspace(t.Context(), "platform", 2)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOrganizationViewAuthorityIsScopedAndDoesNotCreateMembership(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	h.seedMember(t, 3, 33, MemberActive)
	_, err := h.repository.GrantPlatformAdministrator(t.Context(), 33)
	require.NoError(t, err)
	members := NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	for _, kind := range []string{"platform", "tenant"} {
		id := guid.ID(1)
		if kind == "tenant" {
			id = 2
		}
		ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 33, AccountView: &authnext.AccountViewIdentity{ID: 50, PrincipalID: 33, ScopeType: kind, ScopeID: id, TargetType: kind}})
		if kind == "platform" {
			allowed, err := h.authorizer.CheckOperatingPlatform(ctx, id, "role_create", 33)
			require.NoError(t, err)
			require.True(t, allowed)
			allowed, err = h.authorizer.CheckOperatingPlatform(t.Context(), id, "role_create", 33)
			require.NoError(t, err)
			require.False(t, allowed, "ordinary system cookie authority must not bypass platform membership")
		} else {
			allowed, err := h.authorizer.Check(ctx, id, "role_create", 33)
			require.NoError(t, err)
			require.True(t, allowed)
			constraint, err := h.authorizer.Constraint(ctx, id, "file_read", 33)
			require.NoError(t, err)
			require.True(t, constraint.AllowAll, "data constraints must agree with exact organization administration")
			allowed, err = h.authorizer.Check(t.Context(), id, "role_create", 33)
			require.NoError(t, err)
			require.False(t, allowed)
		}
		var count int64
		require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", id, 33).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, h.db.Exec("DELETE FROM sl_iam_platform_administrators WHERE principal_id = ?", 33).Error)
	seedSystemPermissions(t, h, 33, "account_view_create")
	m := &Module{runtime: h.runtime, checker: h.authorizer, members: members, registrar: authz.NewDeclarationOnlyRegistrar(h.service.registry)}
	for _, scope := range []string{"platform", "tenant"} {
		id := guid.ID(1)
		if scope == "tenant" {
			id = 2
		}
		err := h.db.Transaction(func(tx *gorm.DB) error { return m.authorizeAccountView(t.Context(), tx, 33, 0, scope, id) })
		require.ErrorIs(t, err, authnext.ErrInvalidCredentials, "view creation authority alone must not grant organization administration")
	}
}
