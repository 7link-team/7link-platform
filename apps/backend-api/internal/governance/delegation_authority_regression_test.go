package governance

import (
	"context"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDelegationAuthorityFailsClosedAtEveryCurrentSourceFence(t *testing.T) {
	for _, fault := range []string{"invalid kind", "production", "missing source", "principal error", "inactive principal", "inactive member", "agent error", "missing agent", "wrong role", "explain error", "explicit deny", "no grant", "project error", "record source", "corrupt conditions", "incident source", "rules source", "self intake", "missing triage", "own transaction", "invalid transaction", "missing writer"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: "decision", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
			granted, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: 3}, true, nil
			}
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: "decision_approve", SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99", Effect: "allow"}}}, nil
			}
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 3, ActorKind: "AGENT", Code: "decision_approve", Resource: ResourceRef{Kind: "work_item", ID: 77}, Facts: &WorkItemFacts{Type: "TASK", Risk: "LOW", CostMicros: 100}}
			want := ""
			switch fault {
			case "invalid kind":
				req.Resource.Kind = "intake"
				want = "INVALID_REQUEST"
			case "production":
				req.Environment = "PRODUCTION"
			case "missing source":
				m.delegations.projects.InspectOn = nil
			case "principal error":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "inactive principal":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = "PRINCIPAL_INACTIVE"
			case "inactive member":
				m.delegations.members = delegationMembers{false}
				want = "PRINCIPAL_INACTIVE"
			case "agent error":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			case "missing agent":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				want = "PRINCIPAL_INACTIVE"
			case "wrong role":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
				}
				want = "ACTOR_NOT_ELIGIBLE"
			case "explain error":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, ErrUnavailable
				}
			case "explicit deny":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: true, Reason: "explicit_deny"}, nil
				}
				want = "EXPLICIT_DENY"
			case "no grant":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, nil
				}
				want = "NO_ROLE_GRANT"
			case "project error":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, ErrUnavailable
				}
			case "record source":
				require.NoError(t, db.Migrator().DropTable(m.seed.names.Delegations.String()))
			case "corrupt conditions":
				q := query.BindDelegations(db, m.seed.names)
				_, err = q.WithContext(ctx).Where(q.ID.Eq(granted.ID)).Update(q.ConditionsJSON, "broken")
				require.NoError(t, err)
			case "incident source":
				require.NoError(t, db.Migrator().DropTable(m.seed.names.EscalationIncidents.String()))
			case "rules source":
				require.NoError(t, db.Migrator().DropTable(m.seed.names.EscalationRules.String()))
			case "self intake":
				req.Code, req.Resource.Kind, req.Facts.TriageCardAuthorID = "intake_convert", "intake", "3"
				want = "SELF_REVIEW"
			case "missing triage":
				req.Code, req.Resource.Kind = "intake_convert", "intake"
				want = "INVALID_REQUEST"
			case "own transaction":
				want = "DELEGATION_VERIFICATION"
			case "missing writer":
				m.runtime = nil
			}
			var decision AuthorityDecision
			if fault == "own transaction" || fault == "missing writer" {
				decision, err = m.checkDelegationAuthority(ctx, nil, req)
			} else if fault == "invalid transaction" {
				decision, err = m.checkDelegationAuthority(ctx, db, req)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error {
					var err error
					decision, err = m.checkDelegationAuthority(ctx, tx, req)
					return err
				})
			}
			require.False(t, decision.Allowed)
			require.Zero(t, decision.DelegationID)
			if want == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, want, decision.ReasonCode)
			}
			require.EqualValues(t, 1, seedCount(t, db, "binding_probe"), "failed authority reads must not mutate IAM carriers")
			require.EqualValues(t, 1, seedCount(t, db, "event_probe"), "failed authority reads must not invent domain events")
		})
	}
}
