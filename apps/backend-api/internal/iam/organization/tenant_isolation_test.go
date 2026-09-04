package organization

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEveryDirectoryMutationIsTenantScoped(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantActive)
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 2, 20, "Bob")
	members := &organizationMemberChecker{prefix: h.prefix}
	guard := &organizationAdministratorGuard{}

	departmentB, err := h.service.Create(t.Context(), 2, CreateDepartment{Name: "Tenant B Department"})
	require.NoError(t, err)
	_, err = h.service.Get(t.Context(), 1, departmentB.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.Update(t.Context(), 1, departmentB.ID, UpdateDepartment{Name: stringPointer("Changed"), Version: 1})
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, h.service.Delete(t.Context(), 1, departmentB.ID, 1), ErrNotFound)
	storedDepartment, err := h.service.Get(t.Context(), 2, departmentB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B Department", storedDepartment.Name)

	positions := NewPositionService(h.runtime, h.appender, members, guard, newOrganizationTestIDs(1100))
	positionB, err := positions.Create(t.Context(), 2, CreatePosition{Code: "operator", Name: "Operator"})
	require.NoError(t, err)
	_, err = positions.Get(t.Context(), 1, positionB.ID)
	assert.ErrorIs(t, err, ErrPositionNotFound)
	_, err = positions.Update(t.Context(), 1, positionB.ID, UpdatePosition{Name: stringPointer("Changed"), Version: 1})
	assert.ErrorIs(t, err, ErrPositionNotFound)
	assert.ErrorIs(t, positions.Delete(t.Context(), 1, positionB.ID, 1), ErrPositionNotFound)
	_, err = positions.PutMember(t.Context(), 1, positionB.ID, 10, PositionMemberWindow{})
	assert.ErrorIs(t, err, ErrPositionNotFound)
	_, err = positions.DeleteMember(t.Context(), 1, positionB.ID, 20)
	assert.ErrorIs(t, err, ErrPositionNotFound)
	positionB, err = positions.Get(t.Context(), 2, positionB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Operator", positionB.Name)

	groups := NewGroupService(h.runtime, h.appender, members, guard, newOrganizationTestIDs(1200))
	groupB, err := groups.Create(t.Context(), 2, CreateGroup{Name: "Tenant B Group"})
	require.NoError(t, err)
	_, err = groups.Get(t.Context(), 1, groupB.ID)
	assert.ErrorIs(t, err, ErrGroupNotFound)
	_, err = groups.Update(t.Context(), 1, groupB.ID, UpdateGroup{Name: stringPointer("Changed"), Version: 1})
	assert.ErrorIs(t, err, ErrGroupNotFound)
	assert.ErrorIs(t, groups.Delete(t.Context(), 1, groupB.ID, 1), ErrGroupNotFound)
	_, err = groups.PutMember(t.Context(), 1, groupB.ID, 10, GroupMemberWindow{})
	assert.ErrorIs(t, err, ErrGroupNotFound)
	_, err = groups.DeleteMember(t.Context(), 1, groupB.ID, 20)
	assert.ErrorIs(t, err, ErrGroupNotFound)
	groupB, err = groups.Get(t.Context(), 2, groupB.ID)
	require.NoError(t, err)
	assert.Equal(t, "Tenant B Group", groupB.Name)
}

func TestInvitationTokenRemainsBoundToItsPersistedTenant(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	h.seedTenant(t, 2, TenantActive)
	credentials := &invitationCredentialFixture{}
	createdTenants := make([]guid.ID, 0, 1)
	service := NewInvitationService(
		h.runtime, h.appender, newOrganizationTestIDs(1300), credentials,
		func(_ context.Context, _ *gorm.DB, tenantID guid.ID, _, _, _, _ string) (guid.ID, error) {
			createdTenants = append(createdTenants, tenantID)
			return 1400, nil
		},
	)
	invitation, token, err := service.Create(t.Context(), 2, CreateInvitation{Email: "user@example.com"})
	require.NoError(t, err)
	items, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.ErrorIs(t, service.Revoke(t.Context(), 1, invitation.ID), ErrInvitationNotFound)
	_, _, err = service.Resend(t.Context(), 1, invitation.ID, 0)
	assert.ErrorIs(t, err, ErrInvitationNotFound)

	wrongTenantToken := strings.Replace(token, invitation.ID.String(), "1", 1)
	_, err = service.Accept(t.Context(), AcceptInvitation{
		Token: wrongTenantToken, LoginName: "wrong", Password: "correct horse battery staple",
	})
	assert.ErrorIs(t, err, ErrInvitationCredential)
	assert.Empty(t, createdTenants)

	accepted, err := service.Accept(t.Context(), AcceptInvitation{
		Token: token, LoginName: "right", Password: "correct horse battery staple",
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, accepted.TenantID)
	assert.Equal(t, []guid.ID{2}, createdTenants)
}
