package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareMemberExit(t *testing.T, h coreServiceHarness) {
	t.Helper()
	require.NoError(t, h.db.Exec("CREATE TABLE exit_effects (kind TEXT PRIMARY KEY, count INTEGER NOT NULL)").Error)
	for _, kind := range []string{"proof", "pat", "oauth"} {
		require.NoError(t, h.db.Exec("INSERT INTO exit_effects VALUES (?,0)", kind).Error)
	}
	h.service.reauthenticate = func(ctx context.Context, tx *gorm.DB, id guid.ID, cookie, proof, purpose, target string) error {
		if id != 11 || cookie != "browser" || proof != "verified" || purpose != "tenant_leave" || target != "1" {
			return authnext.ErrInvalidCredentials
		}
		return tx.WithContext(ctx).Exec("UPDATE exit_effects SET count = count + 1 WHERE kind = 'proof'").Error
	}
	h.service.revokePersonalTokens = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(11), id)
		return tx.WithContext(ctx).Exec("UPDATE exit_effects SET count = count + 1 WHERE kind = 'pat'").Error
	}
	h.service.revokeOAuth = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(11), id)
		return tx.WithContext(ctx).Exec("UPDATE exit_effects SET count = count + 1 WHERE kind = 'oauth'").Error
	}
}

func TestLeaveTenantPreservesOtherMembershipAndAudits(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedMember(t, 2, 22, MemberActive)
	_, err := h.service.PutTenantMember(t.Context(), 2, 11, "Member", "", 0, MemberActive)
	require.NoError(t, err)
	prepareMemberExit(t, h)
	items, total, err := h.service.MyMemberships(t.Context(), 11, 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 1)
	_, err = h.service.LeaveTenant(t.Context(), 11, 1, "browser", "verified")
	require.NoError(t, err)
	member, err := h.service.GetTenantMember(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, MemberDisabled, member.Status)
	other, err := h.service.GetTenantMember(t.Context(), 2, 11)
	require.NoError(t, err)
	require.Equal(t, MemberActive, other.Status)
	items, total, err = h.service.MyMemberships(t.Context(), 11, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, guid.ID(2), items[0].TenantID)
	var count int
	require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&count).Error)
	require.Equal(t, 3, count)
	require.Equal(t, "tenant_member_status_changed", (*h.events)[len(*h.events)-1].EventType)
}

func TestAdministratorMemberDisableRevokesTenantCredentialsAtomically(t *testing.T) {
	for _, upsert := range []bool{false, true} {
		for _, revokeFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("upsert=%t/revokeFails=%t", upsert, revokeFails), func(t *testing.T) {
				h := newCoreServiceHarness(t)
				h.seedMember(t, 1, 11, MemberActive)
				h.seedMember(t, 2, 22, MemberActive)
				_, err := h.service.PutTenantMember(t.Context(), 2, 11, "Other contact", "other@example.test", 0, MemberActive)
				require.NoError(t, err)
				prepareMemberExit(t, h)
				if revokeFails {
					h.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return errors.New("withdrawal failed") }
				}
				if upsert {
					_, err = h.service.PutTenantMember(t.Context(), 1, 11, "Disabled contact", "changed@example.test", 0, MemberDisabled)
				} else {
					_, err = h.service.SetTenantMemberStatus(t.Context(), 1, 11, MemberDisabled)
				}
				var effects int
				require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&effects).Error)
				member, getErr := h.service.GetTenantMember(t.Context(), 1, 11)
				require.NoError(t, getErr)
				if revokeFails {
					require.ErrorContains(t, err, "withdrawal failed")
					require.Zero(t, effects)
					require.Equal(t, MemberActive, member.Status)
					require.Equal(t, "Member 11", member.DisplayName)
				} else {
					require.NoError(t, err)
					require.Equal(t, 2, effects, "only tenant PAT and OAuth withdrawal runs")
					require.Equal(t, MemberDisabled, member.Status)
				}
				other, getErr := h.service.GetTenantMember(t.Context(), 2, 11)
				require.NoError(t, getErr)
				require.Equal(t, MemberActive, other.Status)
				require.Equal(t, "Other contact", other.DisplayName)
			})
		}
	}
}

func TestAdministratorMemberDisableRequiresOwnerCapabilities(t *testing.T) {
	for _, capability := range []string{"lock", "pat", "oauth"} {
		t.Run(capability, func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			switch capability {
			case "lock":
				h.service.lockPrincipal = nil
			case "pat":
				h.service.revokePersonalTokens = nil
			case "oauth":
				h.service.revokeOAuth = nil
			}
			_, err := h.service.SetTenantMemberStatus(t.Context(), 1, 11, MemberDisabled)
			require.ErrorIs(t, err, ErrUnavailable)
			member, err := h.service.GetTenantMember(t.Context(), 1, 11)
			require.NoError(t, err)
			require.Equal(t, MemberActive, member.Status)
		})
	}
}

func TestAdministratorMemberDisableLastAdminRollsBackWithdrawal(t *testing.T) {
	for _, upsert := range []bool{false, true} {
		t.Run(fmt.Sprintf("upsert=%t", upsert), func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			role, err := h.service.CreateRole(t.Context(), 1, "Admin", "")
			require.NoError(t, err)
			_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "tenant_administer")
			require.NoError(t, err)
			_, err = h.service.AddMember(t.Context(), 1, role.ID, 11)
			require.NoError(t, err)
			prepareMemberExit(t, h)
			if upsert {
				_, err = h.service.PutTenantMember(t.Context(), 1, 11, "Changed", "", 0, MemberDisabled)
			} else {
				_, err = h.service.SetTenantMemberStatus(t.Context(), 1, 11, MemberDisabled)
			}
			require.ErrorIs(t, err, ErrLastTenantAdministrator)
			var effects int
			require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&effects).Error)
			require.Zero(t, effects)
			member, err := h.service.GetTenantMember(t.Context(), 1, 11)
			require.NoError(t, err)
			require.Equal(t, MemberActive, member.Status)
		})
	}
}

func TestLeaveTenantLastAdministratorRollsBackProofAndCredentials(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "Admin", "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "tenant_administer")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)
	prepareMemberExit(t, h)
	_, err = h.service.LeaveTenant(t.Context(), 11, 1, "browser", "verified")
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	member, err := h.service.GetTenantMember(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, MemberActive, member.Status)
	var count int
	require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&count).Error)
	require.Zero(t, count)
}

func TestLeaveTenantCredentialFailureRollsBack(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	prepareMemberExit(t, h)
	want := errors.New("OAuth unavailable")
	h.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return want }
	_, err := h.service.LeaveTenant(t.Context(), 11, 1, "browser", "verified")
	require.ErrorIs(t, err, want)
	var count int
	require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&count).Error)
	require.Zero(t, count)
	member, err := h.service.GetTenantMember(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, MemberActive, member.Status)
}

func TestLeaveTenantMissingOrInactiveMembershipDoesNotConsumeProof(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "disabled"}[present], func(t *testing.T) {
			h := newCoreServiceHarness(t)
			h.seedMember(t, 1, 11, MemberActive)
			prepareMemberExit(t, h)
			if present {
				require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("principal_id = 11").Update("status", "disabled").Error)
			} else {
				require.NoError(t, h.db.Table("sl_iam_tenant_members").Where("principal_id = 11").Delete(map[string]any{}).Error)
			}
			_, err := h.service.LeaveTenant(t.Context(), 11, 1, "browser", "verified")
			require.Error(t, err)
			if present {
				require.ErrorIs(t, err, ErrInvalidArgument)
			} else {
				require.ErrorIs(t, err, ErrMemberNotFound)
			}
			var count int
			require.NoError(t, h.db.Raw("SELECT SUM(count) FROM exit_effects").Scan(&count).Error)
			require.Zero(t, count, "invalid membership must roll back proof and all credential effects")
		})
	}
}

func TestMyMembershipsUnavailableStorageCannotReportAnEmptyAccount(t *testing.T) {
	service := &Service{repo: &Repository{}}
	_, _, err := service.MyMemberships(t.Context(), 11, 0, 50)
	require.ErrorIs(t, err, ErrUnavailable)
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_tenant_members").Error)
	items, _, err := h.service.MyMemberships(t.Context(), 11, 0, 50)
	require.Error(t, err)
	require.Nil(t, items, "a failed membership query cannot become a successful empty list")
}
