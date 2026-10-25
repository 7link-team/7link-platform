package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCodeMergeCurrentDirectHumanAuthority(t *testing.T) {
	seed, db, rt := seedHarnessRuntime(t)
	require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "code", Verb: "merge", Scope: authz.ActionScopeTenant}}, nil))
	q := query.BindRoleSeeds(db, seed.names)
	require.NoError(t, q.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
	allowed, explicit := true, false
	m := &Module{runtime: rt, seed: seed, explain: func(_ context.Context, tx *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.NotNil(t, tx)
		require.Equal(t, "code_merge", req.Permission)
		require.EqualValues(t, 99, req.EntityID)
		x := authz.Explanation{Allowed: allowed, Revision: 17, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: "code_merge", Effect: "allow"}}}
		if explicit {
			x.Reason = "explicit_deny"
		}
		return x, nil
	}}
	req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 12, ActorKind: "HUMAN", Code: "code_merge", Resource: ResourceRef{Kind: "work_item", ID: 77}, Facts: &WorkItemFacts{ClaimID: 900, RequiredVerificationsPassed: true}}
	for _, mode := range []string{"direct", "revoked", "explicit_deny", "carrier"} {
		t.Run(mode, func(t *testing.T) {
			allowed, explicit = mode != "revoked", mode == "explicit_deny"
			if mode == "carrier" {
				_, err := q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(1))).Update(q.RoleKey, "delegated_code_merge")
				require.NoError(t, err)
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				decision, err := m.deliveryAuthority(t.Context(), tx, req)
				require.NoError(t, err)
				require.Equal(t, mode == "direct", decision.Allowed)
				if mode == "direct" {
					require.Equal(t, "ROLE:delivery_lead", decision.AuthoritySource)
				}
				return nil
			}))
		})
	}
}

func TestCodeMergeDelegationUsesLowRiskCostAndReworkBounds(t *testing.T) {
	c := DelegationConditions{Actions: []string{"code_merge"}, MaxRisk: "LOW", CostCapMicros: 42, MaxRework: 3}
	f := WorkItemFacts{Risk: "LOW", CostMicros: 42, QualityReworkCount: 2, RequiredVerificationsPassed: true}
	g := DelegationGuards{ProjectAllowed: true, ClaimEvaluated: true}
	require.Empty(t, delegationConditionReason(c, "code_merge", &f, g))
	for _, tc := range []struct {
		name   string
		mutate func(*WorkItemFacts)
	}{
		{"medium", func(f *WorkItemFacts) { f.Risk = "MEDIUM" }},
		{"cost", func(f *WorkItemFacts) { f.CostMicros++ }},
		{"rework", func(f *WorkItemFacts) { f.QualityReworkCount++ }},
		{"human", func(f *WorkItemFacts) { f.RequireHumanDecision = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := f
			tc.mutate(&changed)
			require.NotEmpty(t, delegationConditionReason(c, "code_merge", &changed, g))
		})
	}
}

func TestCodeMergePersistedDelegationRejectsSelfRiskCostAndRework(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: "code", Verb: "merge", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	roles := query.BindRoleSeeds(db, m.seed.names)
	_, err := roles.WithContext(ctx).Where(roles.ID.Eq(guid.ID(1))).Update(roles.RoleKey, "delegated_code_merge")
	require.NoError(t, err)
	in.Actions = []string{"code_merge"}
	granted, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 3}, true, nil
	}
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{PermissionCode: "code_merge", RoleID: "21", SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99", Effect: "allow"}}}, nil
	}
	receipts := query.BindClaimEvaluations(db, m.seed.names)
	require.NoError(t, receipts.WithContext(ctx).Create(&model.ClaimEvaluation{ID: 900, TenantID: 1, ProjectID: 99, WorkItemID: 77, ClaimID: 777, TriggerEventID: 800, LowConfidenceResult: "PASS", SensitivePathResult: "PASS", EvaluatedAt: m.seed.now()}))
	base := WorkItemFacts{ClaimID: 777, Type: "TASK", Risk: "LOW", CostMicros: 100, QualityReworkCount: 2, RequiredVerificationsPassed: true}
	for _, tc := range []struct {
		name, reason string
		change       func(*WorkItemFacts)
	}{
		{"allowed", "", func(*WorkItemFacts) {}},
		{"self", "SELF_REVIEW", func(f *WorkItemFacts) { f.SelfProduced = true }},
		{"medium", "DELEGATION_RISK", func(f *WorkItemFacts) { f.Risk = "MEDIUM" }},
		{"cost", "DELEGATION_COST", func(f *WorkItemFacts) { f.CostMicros++ }},
		{"rework", "DELEGATION_REWORK", func(f *WorkItemFacts) { f.QualityReworkCount++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := base
			tc.change(&facts)
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 3, ActorKind: "AGENT", Code: "code_merge", Resource: ResourceRef{Kind: "work_item", ID: 77}, Facts: &facts}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				decision, err := m.Check(ctx, tx, req)
				require.NoError(t, err)
				require.Equal(t, tc.reason, decision.ReasonCode)
				require.Equal(t, tc.reason == "", decision.Allowed)
				if decision.Allowed {
					require.Equal(t, granted.ID, decision.DelegationID)
					require.Equal(t, "DELEGATION:"+granted.ID.String(), decision.AuthoritySource)
				}
				return nil
			}))
		})
	}
}
