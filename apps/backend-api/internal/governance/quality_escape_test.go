package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func escapeHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *governanceTestPublisher, guid.ID) {
	t.Helper()
	m, db, ctx, pub, _ := delegationHarness(t)
	m.principals.LockActiveRow = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, true, nil
	}
	data, _ := json.Marshal(map[string]any{"escape_key": "work:44:claim:55", "work_item_id": "44", "stage": "HUMAN_REVIEW", "category": "FUNCTIONAL_DEFECT", "summary": "Rounded refund differed from criterion", "passed_by": []PassedGate{{Stage: "AGENT_TEST", PrincipalID: 3, ModelFamily: "family-a", VerifierVersion: "v1"}}})
	event := events.Envelope{EventID: 99, TenantID: 1, ProjectID: 99, EventType: "quality.escaped", Data: data}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, event) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeQualityEscape(ctx, tx, event) }))
	q := query.BindQualityEscapes(db, m.seed.names)
	r, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	require.Equal(t, "PENDING", r.LearningState)
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.QualityEscapes.String()))
	return m, db, ctx, pub, r.ID
}
func TestQualityEscapeSourceReplayHumanDecisionCASAndAtomicOutbox(t *testing.T) {
	m, db, ctx, pub, id := escapeHarness(t)
	decide := func(version int, state, reason string) error {
		return db.Transaction(func(tx *gorm.DB) error { _, e := m.decideEscapeOn(ctx, tx, 1, id, version, state, reason); return e })
	}
	require.ErrorIs(t, decide(1, "DISMISSED", ""), ErrInvalidArgument)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.ErrorIs(t, decide(1, "RECORDED", ""), ErrForbidden)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 2}, true, nil
	}
	pub.Fail = true
	require.Error(t, decide(1, "RECORDED", ""))
	q := query.BindQualityEscapes(db, m.seed.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "PENDING", r.LearningState)
	require.EqualValues(t, 0, seedCount(t, db, "audit_probe"))
	pub.Fail = false
	require.NoError(t, decide(1, "DISMISSED", "Source criterion was superseded"))
	require.ErrorIs(t, decide(1, "RECORDED", ""), ErrVersion)
	r, e = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "DISMISSED", r.LearningState)
	require.Equal(t, 2, r.Version)
	require.EqualValues(t, 2, *r.DecidedBy)
}
func TestQualityEscapeLearningReferencesAreSourceOwnedAndFrozen(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	consume := func(ref string) error {
		data, _ := json.Marshal(map[string]any{"escape_key": "work:44:claim:55", "memory_ids": []string{"101"}, "eval_case_ref": ref})
		return db.Transaction(func(tx *gorm.DB) error {
			return m.consumeQualityEscape(ctx, tx, events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99, EventType: "quality.escape_learning_proposed", Data: data})
		})
	}
	require.NoError(t, consume("eval:1"))
	require.NoError(t, consume("eval:1"))
	require.ErrorIs(t, consume("eval:2"), ErrVersion)
	q := query.BindQualityEscapes(db, m.seed.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	v, e := escapeView(r)
	require.NoError(t, e)
	require.Equal(t, []guid.ID{101}, v.MemoryIDs)
	require.Equal(t, "PENDING", v.LearningState)
	require.ErrorIs(t, m.consumeQualityEscape(ctx, db, events.Envelope{}), ErrInvalidArgument)
}

func TestQualityEscapeInboxRechecksActionAuthorityAndCurrentLearningState(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	req := InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 99, Kind: "QUALITY_ESCAPE", Limit: 1}
	rows, err := m.qualityEscapeInbox(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, id, rows[0].ObjectID)
	require.Equal(t, "quality_escape", rows[0].ObjectType)
	req.After = &rows[0].InboxKey
	rows, err = m.qualityEscapeInbox(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows)
	req.After = nil
	inspect := m.delegations.projects.InspectOn
	for _, denied := range []error{ErrForbidden, ErrNotFound} {
		m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
			return DelegationProjectState{}, denied
		}
		rows, err = m.qualityEscapeInbox(ctx, req)
		require.NoError(t, err)
		require.Empty(t, rows, "revoked or archived projects are not actionable")
	}
	m.delegations.projects.InspectOn = inspect
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.decideEscapeOn(ctx, tx, 1, id, 1, "RECORDED", ""); return e }))
	rows, err = m.qualityEscapeInbox(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows)
	req.TenantID = 2
	_, err = m.qualityEscapeInbox(ctx, req)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestQualityEscapeAutomaticDraftIsFrozenSourceProposalAndNeverApproval(t *testing.T) {
	m, db, ctx, _, id := escapeHarness(t)
	q := query.BindQualityEscapes(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	v, err := escapeView(row)
	require.NoError(t, err)
	require.NotNil(t, v.LearningDraft)
	require.Contains(t, v.LearningDraft.Body, "event: 99; work item: 44")
	require.Contains(t, v.LearningDraft.Body, row.Summary)
	require.Contains(t, v.LearningDraft.Body, "A Human must review")
	require.Equal(t, "PENDING", v.LearningState)
	require.Empty(t, v.MemoryIDs)
	require.Nil(t, v.DecidedBy)
	before := *row.LearningDraft
	data, err := json.Marshal(map[string]any{"escape_key": row.EscapeKey, "work_item_id": "44", "summary": "replacement must not rewrite source"})
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.consumeQualityEscape(ctx, tx, events.Envelope{EventID: 100, TenantID: 1, ProjectID: 99, EventType: "quality.escaped", Data: data})
	}))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, before, *row.LearningDraft)
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	row.LearningDraft = nil // Existing rows migrate with NULL; retain compatibility.
	v, err = escapeView(row)
	require.NoError(t, err)
	require.Nil(t, v.LearningDraft)
	for _, invalid := range []string{"{", `{}`, `{"title":"incomplete"}`} {
		row.LearningDraft = &invalid
		_, err = escapeView(row)
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestQualityEscapeAutomaticDraftRollsBackWithSourceOutbox(t *testing.T) {
	m, db, ctx, pub, _ := escapeHarness(t)
	pub.Fail = true
	data, err := json.Marshal(map[string]any{"escape_key": "new-escape", "work_item_id": "44", "stage": "HUMAN_REVIEW", "category": "FUNCTIONAL_DEFECT", "summary": "actual source defect", "passed_by": []PassedGate{{Stage: "AGENT_TEST", PrincipalID: 3, ModelFamily: "a", VerifierVersion: "v1"}}})
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.consumeQualityEscape(ctx, tx, events.Envelope{EventID: 101, TenantID: 1, ProjectID: 99, EventType: "quality.escaped", Data: data})
	}))
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.QualityEscapes.String()))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
}

func TestQualityReviewBindingFreezesSourceAndUsesCurrentSummaryWindow(t *testing.T) {
	m, _, ctx, _, _ := escapeHarness(t)
	require.ErrorIs(t, m.bindQualityReviews(nil), ErrInvalidArgument)
	calls := 0
	require.NoError(t, m.bindQualityReviews(func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time) (QualityReviewWindow, error) {
		calls++
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.Equal(t, start.AddDate(0, 0, 7), end)
		return QualityReviewWindow{Entered: 3}, nil
	}))
	require.ErrorIs(t, m.bindQualityReviews(func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error) {
		t.Fatal("replacement must not run")
		return QualityReviewWindow{}, nil
	}), ErrInvalidArgument)
	out, err := m.qualityEscapeSummaryHTTP(ctx, &escapeSummaryInput{TenantID: "1", ProjectID: "99"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, 3, out.Body.Data.EnteredHumanReview)
	require.Nil(t, out.Body.Data.Rate)
}
func TestQualityEscapeCommandStoreFailuresPreservePendingSource(t *testing.T) {
	for _, scenario := range []string{"read", "update", "audit", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, id := escapeHarness(t)
			fault := errors.New("escape storage failed")
			want := fault
			table := m.seed.names.QualityEscapes.String()
			switch scenario {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:escape", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:escape", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "unavailable":
				m.publisher = nil
				want = ErrUnavailable
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.decideEscapeOn(ctx, tx, 1, id, 1, "RECORDED", ""); return err })
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("test:escape"))
			require.NoError(t, db.Callback().Update().Remove("test:escape"))
			q := query.BindQualityEscapes(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
			require.NoError(t, err)
			require.Equal(t, "PENDING", row.LearningState)
			require.Equal(t, 1, row.Version)
		})
	}
}
