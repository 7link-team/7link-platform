package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestDelegationAuthorityRequiresCurrentRecordCarrierAndEveryCondition(t *testing.T) {
	for _, code := range []string{"intake_convert", "decision_approve"} {
		t.Run(code, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			resource, verb, kind := "decision", "approve", "work_item"
			roleID := guid.ID(21)
			if code == "intake_convert" {
				resource, verb, kind = "intake", "convert", "intake"
				roleID = 22
				q := query.BindRoleSeeds(db, m.seed.names)
				require.NoError(t, q.WithContext(ctx).Create(&model.RoleSeed{ID: 2, TenantID: 1, RoleID: roleID, RoleKey: "delegated_intake_convert", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: m.seed.now(), UpdatedAt: m.seed.now()}))
			}
			require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: resource, Verb: verb, Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
			in.Actions = []string{code}
			granted, e := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, e)
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: 3}, true, nil
			}
			carrier := true
			m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				if r.PrincipalID != 3 {
					return authz.Explanation{Allowed: true, Reason: "administrator_grant"}, nil
				}
				matches := []authz.DecisionMatch{}
				if carrier {
					matches = append(matches, authz.DecisionMatch{PermissionCode: code, RoleID: roleID.String(), SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99", Effect: "allow"})
				}
				return authz.Explanation{Allowed: true, Revision: 9, Matches: matches}, nil
			}
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 3, ActorKind: "AGENT", Code: code, Resource: ResourceRef{Kind: kind, ID: 77}, Facts: &WorkItemFacts{Type: "TASK", Risk: "LOW", CostMicros: 100, RequiredVerificationsPassed: true, IntakeConvertMode: "NEW_REQUIREMENT", TriageCardAuthorID: "2"}}
			check := func() AuthorityDecision {
				var out AuthorityDecision
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.Check(ctx, tx, req); return e }))
				return out
			}
			out := check()
			if code == "intake_convert" {
				require.True(t, out.Allowed)
				require.Equal(t, "DELEGATION:"+granted.ID.String(), out.AuthoritySource)
				require.Equal(t, granted.ID, out.DelegationID)
			} else {
				require.False(t, out.Allowed)
				require.Equal(t, "ESCALATION_PENDING", out.ReasonCode, "verification flags cannot replace a persisted escalation receipt")
				req.Facts.ClaimID = 777
				require.Equal(t, "ESCALATION_PENDING", check().ReasonCode)
				receipts := query.BindClaimEvaluations(db, m.seed.names)
				require.NoError(t, receipts.WithContext(ctx).Create(&model.ClaimEvaluation{ID: 900, TenantID: 1, ProjectID: 99, WorkItemID: 77, ClaimID: 777, TriggerEventID: 800, LowConfidenceResult: "PASS", SensitivePathResult: "PASS", EvaluatedAt: m.seed.now()}))
				require.True(t, check().Allowed, "only exact persisted claim receipt satisfies S6")
				req.Facts.ClaimID = 778
				require.Equal(t, "ESCALATION_PENDING", check().ReasonCode)
				req.Facts.ClaimID = 777
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.recordEscalationOn(ctx, tx, EscalationFact{TenantID: 1, ProjectID: 99, SubjectID: 77, TriggerEventID: 801, Kind: "SENSITIVE_PATH", SubjectKind: "WORK_ITEM", Reason: "Sensitive files changed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:escalation_evaluator"}})
					return e
				}))
				require.Equal(t, "ESCALATION_HOLD", check().ReasonCode, "receipt cannot bypass its incident")
				incidents := query.BindEscalationIncidents(db, m.seed.names)
				_, e = incidents.WithContext(ctx).Where(incidents.TenantID.Eq(guid.ID(1))).Update(incidents.State, "DISMISSED")
				require.NoError(t, e)
				require.True(t, check().Allowed)
			}
			carrier = false
			require.Equal(t, "NO_DELEGATION", check().ReasonCode)
			carrier = true
			req.Facts.SelfProduced = true
			require.Equal(t, "SELF_REVIEW", check().ReasonCode)
			req.Facts.SelfProduced = false
			if code == "decision_approve" {
				req.Facts.Risk = "HIGH"
				require.Equal(t, "DELEGATION_RISK", check().ReasonCode)
				req.Facts.Risk = "LOW"
			}
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Mode: "HYBRID"}, nil
			}
			require.Equal(t, "PROJECT_GATE", check().ReasonCode)
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Mode: "AUTONOMOUS", DelegatedAllowed: true}, nil
			}
			at := granted.EndsAt
			m.seed.now = func() time.Time { return at }
			require.Equal(t, "NO_DELEGATION", check().ReasonCode)
		})
	}
}
