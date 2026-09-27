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
