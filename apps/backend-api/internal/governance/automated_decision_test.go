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
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAutomatedDecisionUnavailableAndSupersededReceiptsReplayAtomically(t *testing.T) {
	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "unavailable", false: "superseded"}[current], func(t *testing.T) {
			m, db, e, pub := claimEvaluationHarness(t)
			calls := 0
			require.NoError(t, m.bindAutomatedDecisions(AutomatedDecisionSource{ResolveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (AuthorityRequest, bool, error) {
				calls++
				return AuthorityRequest{}, current, nil
			}}))
			require.ErrorIs(t, m.bindAutomatedDecisions(m.automatedDecisions), ErrInvalidArgument)
			run := func() error {
				return db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomatedDecision(t.Context(), tx, e) })
			}
			pub.Fail = true
			require.Error(t, run())
			require.Zero(t, seedCount(t, db, m.seed.names.AutomatedDecisions.String()))
			require.Zero(t, seedCount(t, db, m.seed.names.ClaimEvaluations.String()))
			require.Zero(t, seedCount(t, db, "audit_probe"))
			pub.Fail = false
			require.NoError(t, run())
			require.NoError(t, run())
			require.Equal(t, 1, calls)
			require.EqualValues(t, 1, seedCount(t, db, m.seed.names.AutomatedDecisions.String()))
			q := query.BindAutomatedDecisions(db, m.seed.names)
			r, err := q.WithContext(t.Context()).First()
			require.NoError(t, err)
			require.Equal(t, map[bool]string{true: "UNAVAILABLE", false: "SUPERSEDED"}[current], r.State)
			require.Zero(t, r.DecisionID)
			require.Empty(t, r.AuthoritySource)
			e.ProjectID = 100
			require.ErrorIs(t, run(), ErrInvalidArgument)
		})
	}
}

func TestAutomatedDecisionAppliesOnlyCurrentDelegationAndRollsBackCommandAuditOutbox(t *testing.T) {
	m, db, ctx, pub, input := delegationHarness(t)
	require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: "decision", Verb: "approve", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	granted, err := grantDelegation(t, m, db, ctx, input)
	require.NoError(t, err)
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 3}, true, nil
	}
	revoked := false
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: !revoked, Revision: 9, Matches: []authz.DecisionMatch{{PermissionCode: "decision_approve", RoleID: "21", SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99", Effect: "allow"}}}, nil
	}
	require.NoError(t, query.BindClaimEvaluations(db, m.seed.names).WithContext(ctx).Create(&model.ClaimEvaluation{ID: 901, TenantID: 1, ProjectID: 99, WorkItemID: 77, ClaimID: 777, TriggerEventID: 800, LowConfidenceResult: "PASS", SensitivePathResult: "PASS", EvaluatedAt: m.seed.now()}))
	m.claimSource.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error) {
		t.Fatal("exact claim already evaluated")
		return ClaimEvaluationFacts{}, false, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE decision_probe (id BIGINT)").Error)
	request := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 3, ActorKind: "AGENT", Code: "decision_approve", Resource: ResourceRef{Kind: "work_item", ID: 77}, Facts: &WorkItemFacts{ClaimID: 777, Type: "TASK", Risk: "LOW", CostMicros: 100, RequiredVerificationsPassed: true}}
	applies := 0
	m.automatedDecisions = AutomatedDecisionSource{ResolveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (AuthorityRequest, bool, error) {
		return request, true, nil
	}, ApplyOn: func(_ context.Context, tx *gorm.DB, r AuthorityRequest, a AuthorityDecision) (guid.ID, error) {
		applies++
		require.Equal(t, granted.ID, a.DelegationID)
		require.EqualValues(t, 3, r.PrincipalID)
		return 123, tx.Exec("INSERT INTO decision_probe VALUES (123)").Error
	}}
	e := events.Envelope{EventID: 800, TenantID: 1, ProjectID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: 77}, Data: []byte(`{"to":"NEEDS_HUMAN_REVIEW","result_id":"777"}`)}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomatedDecision(ctx, tx, e) })
	}
	pub.Fail = true
	require.Error(t, run())
	require.Equal(t, 1, applies)
	require.Zero(t, seedCount(t, db, "decision_probe"))
	require.Zero(t, seedCount(t, db, m.seed.names.AutomatedDecisions.String()))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"), "only initial grant remains")
	pub.Fail = false
	revoked = true
	require.NoError(t, run())
	require.Equal(t, 1, applies, "current IAM denial never invokes source command")
	q := query.BindAutomatedDecisions(db, m.seed.names)
	row, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "DENIED", row.State)
	// A separate exact claim exercises successful application and replay without
	// turning a denied receipt into a later success under changed authority.
	revoked = false
	e.Data = []byte(`{"to":"NEEDS_HUMAN_REVIEW","result_id":"778"}`)
	request.Facts.ClaimID = 778
	require.NoError(t, query.BindClaimEvaluations(db, m.seed.names).WithContext(ctx).Create(&model.ClaimEvaluation{ID: 902, TenantID: 1, ProjectID: 99, WorkItemID: 77, ClaimID: 778, TriggerEventID: 801, LowConfidenceResult: "PASS", SensitivePathResult: "PASS", EvaluatedAt: m.seed.now()}))
	require.NoError(t, run())
	require.NoError(t, run())
	require.Equal(t, 2, applies)
	require.EqualValues(t, 1, seedCount(t, db, "decision_probe"))
	row, err = q.WithContext(ctx).Where(q.ClaimID.Eq(guid.ID(778))).First()
	require.NoError(t, err)
	require.Equal(t, "APPLIED", row.State)
	require.EqualValues(t, 123, row.DecisionID)
	require.Equal(t, "DELEGATION:"+granted.ID.String(), row.AuthoritySource)
}

func TestAutomatedDecisionRejectsUntrustedScopeAndPropagatesSourceFailure(t *testing.T) {
	m, db, e, _ := claimEvaluationHarness(t)
	m.automatedDecisions = AutomatedDecisionSource{ResolveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (AuthorityRequest, bool, error) {
		return AuthorityRequest{TenantID: 2}, true, nil
	}, ApplyOn: func(context.Context, *gorm.DB, AuthorityRequest, AuthorityDecision) (guid.ID, error) {
		t.Fatal("foreign scope must never execute")
		return 0, nil
	}}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomatedDecision(t.Context(), tx, e) })
	}
	require.ErrorIs(t, run(), ErrInvalidArgument)
	failure := errors.New("source unavailable")
	m.automatedDecisions.ResolveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (AuthorityRequest, bool, error) {
		return AuthorityRequest{}, false, failure
	}
	require.ErrorIs(t, run(), failure)
	require.Zero(t, seedCount(t, db, m.seed.names.AutomatedDecisions.String()))
	require.ErrorIs(t, m.consumeAutomatedDecision(t.Context(), db, e), ErrInvalidArgument)
}

func TestAppliedDelegationActivityRequiresSuccessfulReceiptAndCurrentRead(t *testing.T) {
	m, db, ctx, _, _ := delegationHarness(t)
	at := m.seed.now().UTC()
	allowed := true
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "project_read", req.Permission)
		require.EqualValues(t, 99, req.EntityID)
		return authz.Explanation{Allowed: allowed}, nil
	}
	q := query.BindAutomatedDecisions(db, m.seed.names)
	for i, state := range []string{"APPLIED", "DENIED", "UNAVAILABLE", "SUPERSEDED", "APPLIED", "APPLIED"} {
		decision := guid.ID(123)
		if i == 4 {
			decision = 0
		}
		project := guid.ID(99)
		if i == 5 {
			project = 100
		}
		require.NoError(t, q.WithContext(ctx).Create(&model.AutomatedDecision{ID: guid.ID(500 + i), TenantID: 1, ProjectID: project, WorkItemID: 77, ClaimID: guid.ID(700 + i), TriggerEventID: guid.ID(800 + i), State: state, Reason: "activity fixture", ActorID: 3, AuthoritySource: "DELEGATION:1", DecisionID: decision, CreatedAt: at}))
	}
	read := func() ([]AppliedDelegationFact, error) {
		return m.appliedDelegationsOn(ctx, db, 1, 99, at.Add(-time.Minute), at.Add(time.Minute))
	}
	rows, err := read()
	require.NoError(t, err)
	require.Equal(t, []AppliedDelegationFact{{WorkItemID: 77, ActorID: 3, At: at}}, rows)
	allowed = false
	_, err = read()
	require.ErrorIs(t, err, ErrForbidden)
	allowed = true
	rows, err = m.appliedDelegationsOn(ctx, db, 1, 99, at.Add(time.Minute), at.Add(2*time.Minute))
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = m.appliedDelegationsOn(ctx, db, 2, 99, at.Add(-time.Minute), at.Add(time.Minute))
	require.ErrorIs(t, err, ErrForbidden)
}
