package governance

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDispatchAutoPolicyUsesCurrentLeadIdentityAndExplicitDenyFences(t *testing.T) {
	for _, scenario := range []string{"policy", "human", "other_agent", "wrong_project", "revoked", "high_risk", "hold", "manual_policy", "human_only", "self", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			seed, db, rt := seedHarnessRuntime(t)
			require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "work_item", Verb: "assign", Scope: authz.ActionScopeTenant}}, nil))
			m := &Module{runtime: rt, seed: seed}
			m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				if scenario == "revoked" {
					return authz.Explanation{Reason: "explicit_deny"}, nil
				}
				return authz.Explanation{Allowed: r.Permission == "work_item_read" && scenario != "unreadable"}, nil
			}
			m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				principal := guid.ID(11)
				if scenario == "other_agent" {
					principal = 12
				}
				return fleet.AgentIdentity{ID: 30, TenantID: 1, PrincipalID: principal, State: "ACTIVE", Roles: []string{"LEAD"}}, scenario != "human", nil
			}
			s := module.NewServices()
			var check DispatchAutoAuthority
			require.NoError(t, module.Provide[DispatchAutoConsumer](s, DispatchAutoConsumer{Bind: func(c DispatchAutoAuthority) error { check = c; return nil }}))
			require.NoError(t, s.Seal())
			require.NoError(t, m.bindAutoDispatch(s))
			require.NoError(t, m.policies.seal(seed.registry))
			ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11, TenantID: 1, EntityID: 99, SubjectType: authn.SubjectTypePrincipal})
			f := DispatchAutoFacts{TenantID: 1, ProjectID: 99, WorkItemID: 77, PrincipalID: 11, AgentID: 30, LeadAgentID: 30, Policy: "AUTO", TeamMode: "HYBRID", Stage: "ACTIVE", Risk: "LOW", WorkType: "TASK", ActiveLead: true, AssigneePrincipalID: 21}
			switch scenario {
			case "wrong_project":
				f.ProjectID = 100
			case "high_risk":
				f.Risk = "HIGH"
			case "hold":
				f.Held = true
			case "manual_policy":
				f.Policy = "MANUAL"
			case "human_only":
				f.TeamMode = "HUMAN_ONLY"
			case "self":
				f.AssigneePrincipalID = 11
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				result, err := check(ctx, tx, f)
				require.NoError(t, err)
				require.Equal(t, scenario == "policy", result.Allowed)
				if result.Allowed {
					require.Equal(t, "POLICY:dispatch_auto", result.AuthoritySource)
				}
				return nil
			}))
		})
	}
}

func TestDispatchAuthorityRequiresCurrentDirectHumanRole(t *testing.T) {
	seed, db, rt := seedHarnessRuntime(t)
	require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "work_item", Verb: "assign", Scope: authz.ActionScopeTenant}}, nil))
	q := query.BindRoleSeeds(db, seed.names)
	require.NoError(t, q.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
	allowed := true
	m := &Module{runtime: rt, seed: seed, explain: func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "work_item_assign", r.Permission)
		return authz.Explanation{Allowed: allowed, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: r.Permission, Effect: "allow"}}}, nil
	}}
	req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 11, ActorKind: "HUMAN", Code: "work_item_assign", Resource: ResourceRef{Kind: "work_item", ID: 77}, Facts: &WorkItemFacts{}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		got, e := m.Check(t.Context(), tx, req)
		require.NoError(t, e)
		require.True(t, got.Allowed)
		require.Equal(t, "ROLE:delivery_lead", got.AuthoritySource)
		allowed = false
		got, e = m.Check(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, got.Allowed)
		req.ActorKind = "AGENT"
		got, e = m.Check(t.Context(), tx, req)
		require.NoError(t, e)
		require.False(t, got.Allowed)
		return nil
	}))
}
