package governance

import (
	"7link.com/platform/internal/iam/audit"
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowDecisionsAreImmutableScopedAndReauthorize(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	human := true
	allow := true
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	m.principals.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, human, nil
	}
	m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "workflow_gate_team", r.Permission)
		require.Equal(t, guid.ID(99), r.EntityID)
		return authz.Explanation{Allowed: allow, Reason: "administrator_grant"}, nil
	}
	input := WorkflowDecisionInput{WorkflowDecisionRef: WorkflowDecisionRef{TenantID: 1, ProjectID: 99, BindingID: 123, DefinitionHash: strings.Repeat("a", 64), ExpansionKey: "gate/one", Gate: "TEAM"}, Outcome: "APPROVE", Reason: "Reviewed actual output"}
	record := func() (WorkflowDecisionFact, error) {
		var out WorkflowDecisionFact
		e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.recordWorkflowDecisionOn(ctx, tx, input); return e })
		return out, e
	}
	first, e := record()
	require.NoError(t, e)
	require.Positive(t, first.ID)
	require.Equal(t, "ROLE:tenant_administrator", first.AuthoritySource)
	replay, e := record()
	require.NoError(t, e)
	require.Equal(t, first, replay)
	input.Outcome = "REJECT"
	_, e = record()
	require.ErrorIs(t, e, ErrVersion)
	input.Outcome = "APPROVE"
	allow = false
	_, e = record()
	require.ErrorIs(t, e, ErrForbidden)
	allow = true
	human = false
	_, e = record()
	require.ErrorIs(t, e, ErrForbidden)
	human = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		other := input.WorkflowDecisionRef
		other.DefinitionHash = strings.Repeat("b", 64)
		_, found, e := m.readWorkflowDecisionOn(ctx, tx, other)
		require.False(t, found)
		return e
	}))
	input.ExpansionKey = "gate/two"
	pub.Fail = true
	_, e = record()
	require.Error(t, e)
	q := query.BindWorkflowDecisions(db, m.seed.names)
	count, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)

}

func TestWorkflowDecisionSourceFailuresLeaveNoFrozenDecision(t *testing.T) {
	for _, scenario := range []string{"lock", "human", "explain", "read", "identifier", "zero_identifier", "insert", "audit", "unavailable", "missing_authority"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := delegationHarness(t)
			fault := errors.New("workflow decision source unavailable")
			want := fault
			m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{ID: id}, true, nil
			}
			in := WorkflowDecisionInput{WorkflowDecisionRef: WorkflowDecisionRef{TenantID: 1, ProjectID: 99, BindingID: 123, DefinitionHash: strings.Repeat("a", 64), ExpansionKey: "gate/one", Gate: "TEAM"}, Outcome: "APPROVE", Reason: "Reviewed immutable work result"}
			switch scenario {
			case "lock":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "explain":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:workflow", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.WorkflowDecisions.String() {
						tx.AddError(fault)
					}
				}))
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:workflow", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.WorkflowDecisions.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "unavailable":
				m.publisher = nil
				want = ErrUnavailable
			case "missing_authority":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: true}, nil
				}
				want = ErrForbidden
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.recordWorkflowDecisionOn(ctx, tx, in); return err })
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("test:workflow"))
			require.NoError(t, db.Callback().Create().Remove("test:workflow"))
			require.Zero(t, seedCount(t, db, m.seed.names.WorkflowDecisions.String()))
			require.Zero(t, seedCount(t, db, "audit_probe"))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
}
