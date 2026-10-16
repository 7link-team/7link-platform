package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type memberCheck func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)

func (f memberCheck) IsMemberActive(ctx context.Context, tenant, principal guid.ID) (bool, error) {
	return f(ctx, nil, tenant, principal)
}
func (f memberCheck) IsMemberActiveOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (bool, error) {
	return f(ctx, tx, tenant, principal)
}

func TestProjectMembersResolveCreatorAndHumanRolesInCallerTransaction(t *testing.T) {
	_, db, _ := projectHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		m := &Module{
			members: memberCheck(func(_ context.Context, got *gorm.DB, tenant, _ guid.ID) (bool, error) {
				require.Same(t, tx, got)
				require.Equal(t, guid.ID(7), tenant)
				return true, nil
			}),
			principals: identity.PrincipalStateStore{ActiveHumanByID: func(_ context.Context, got *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				require.Same(t, tx, got)
				return identity.Principal{ID: id}, true, nil
			}},
			roles: governance.SeededRoles{RoleIDOn: func(_ context.Context, got *gorm.DB, tenant guid.ID, key string) (guid.ID, error) {
				require.Same(t, tx, got)
				require.Equal(t, guid.ID(7), tenant)
				return map[string]guid.ID{"delivery_lead": 101, "engineer": 102}[key], nil
			}},
		}
		input := []projectMemberInput{{PrincipalID: 1, RoleKey: "delivery_lead"}, {PrincipalID: 2, RoleKey: "engineer"}, {PrincipalID: 2, RoleKey: "engineer"}, {PrincipalID: 1, RoleKey: "engineer"}}
		result, err := m.projectMembersOn(t.Context(), tx, 7, 1, input)
		require.NoError(t, err)
		require.Equal(t, []projectMember{{1, 101}, {2, 102}, {1, 102}}, result)
		require.Len(t, input, 4)
		_, err = m.projectMembersOn(t.Context(), db, 7, 1, nil)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.projectMembersOn(t.Context(), tx, 7, 1, make([]projectMemberInput, 51))
		require.ErrorIs(t, err, ErrInvalidArgument)
		for _, key := range []string{"team_owner", "agent_executor", "tenant_administrator", "", "Engineer"} {
			result, err = m.projectMembersOn(t.Context(), tx, 7, 1, []projectMemberInput{{PrincipalID: 2, RoleKey: key}})
			require.ErrorIs(t, err, ErrInvalidMember)
			require.Nil(t, result)
		}
		// A real directory identity and matching role are required for nonhumans.
		humanReader := m.principals.ActiveHumanByID
		m.principals.ActiveHumanByID = func(ctx context.Context, got *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
			if id == 2 {
				return identity.Principal{}, false, nil
			}
			return humanReader(ctx, got, id)
		}
		m.agents = fleet.AgentDirectory{ByPrincipalOn: func(_ context.Context, got *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
			require.Same(t, tx, got)
			require.Equal(t, guid.ID(7), tenant)
			require.Equal(t, guid.ID(2), id)
			return fleet.AgentIdentity{ID: 22, TenantID: tenant, PrincipalID: id, IAMRoleKeys: []string{"agent_executor"}}, true, nil
		}}
		originalRoles := m.roles.RoleIDOn
		m.roles.RoleIDOn = func(ctx context.Context, got *gorm.DB, tenant guid.ID, key string) (guid.ID, error) {
			if key == "agent_executor" {
				return 103, nil
			}
			return originalRoles(ctx, got, tenant, key)
		}
		result, err = m.projectMembersOn(t.Context(), tx, 7, 1, []projectMemberInput{{PrincipalID: 2, RoleKey: "agent_executor"}})
		require.NoError(t, err)
		require.Equal(t, []projectMember{{1, 101}, {2, 103}}, result)
		for _, key := range []string{"engineer", "delivery_lead", "agent_reviewer", "delegated_project_manage"} {
			_, err = m.projectMembersOn(t.Context(), tx, 7, 1, []projectMemberInput{{PrincipalID: 2, RoleKey: key}})
			require.ErrorIs(t, err, ErrInvalidMember)
		}
		m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, nil
		}
		_, err = m.projectMembersOn(t.Context(), tx, 7, 1, []projectMemberInput{{PrincipalID: 2, RoleKey: "agent_executor"}})
		require.ErrorIs(t, err, ErrInvalidMember, "a service account without an Agent record must be rejected")
		m.principals.ActiveHumanByID = humanReader
		m.agents = fleet.AgentDirectory{}

		failure := errors.New("reference read failed")
		m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 0, failure }
		result, err = m.projectMembersOn(t.Context(), tx, 7, 1, nil)
		require.ErrorIs(t, err, failure)
		require.Nil(t, result)
		m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, nil
		}
		_, err = m.projectMembersOn(t.Context(), tx, 7, 1, nil)
		require.ErrorIs(t, err, ErrUnavailable, "must not classify service accounts as human or Agent")
		m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
		_, err = m.projectMembersOn(t.Context(), tx, 7, 1, nil)
		require.ErrorIs(t, err, ErrInvalidMember)
		m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
		_, err = m.projectMembersOn(t.Context(), tx, 7, 1, nil)
		require.ErrorIs(t, err, failure)
		return nil
	}))
}
