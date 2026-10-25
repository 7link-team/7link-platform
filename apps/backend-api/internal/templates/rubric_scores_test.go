package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func rubricScoreFixture(t *testing.T) (*Module, *gorm.DB, context.Context, delivery.RubricSubject, RubricScoreInput) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Parameters = map[string]any{"threshold": 70}
	rubric, scores := rubricFixture()
	d.Metadata["rubric"] = rubric
	d.Metadata["verifiers"] = []VerifierDefinition{verifierDefinition()}
	v := publishRuntime(t, m, db, ctx, "rubric-records", d)
	m.delivery.CreateWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.TemplateWorkInput) (guid.ID, error) {
		return 700, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	target := delivery.RubricSubject{ProjectID: 99, WorkItemID: 700, ResultID: 701, Version: 3, ResultVersion: 2, HeadCommit: strings.Repeat("a", 40), Authority: "ROLE:reviewer", ActorKind: "HUMAN", EvidenceIDs: []guid.ID{702}}
	in := RubricScoreInput{Stage: "HUMAN_REVIEW", ResultID: 701, WorkVersion: 3}
	for _, score := range scores {
		in.Scores = append(in.Scores, RubricScoreValue{RubricDimensionScore: score, EvidenceRefs: []guid.ID{702}, Note: "Examined pinned original artifact"})
	}
	m.rubricProof = func(_ context.Context, _ *gorm.DB, in RubricProofRequest) (RubricProofFact, error) {
		require.Equal(t, guid.ID(701), in.ResultID)
		require.Equal(t, 2, in.ResultVersion)
		require.Equal(t, []string{"unit-tests"}, in.VerifierKeys)
		return RubricProofFact{TaskID: 703, ReceiptHash: strings.Repeat("b", 64), VerifierKey: "unit-tests", Passed: true}, nil
	}
	m.rubricSubjects.WorkOn = func(_ context.Context, _ *gorm.DB, p, w, r guid.ID, v int, stage string) (delivery.RubricSubject, error) {
		if p != 99 || w != 700 {
			return delivery.RubricSubject{}, ErrNotFound
		}
		if stage != "" && (r != 701 || v != 3) {
			return delivery.RubricSubject{}, delivery.ErrWorkVersion
		}
		if stage == "AGENT_ACCEPTANCE" {
			return delivery.RubricSubject{}, delivery.ErrWorkForbidden
		}
		return target, nil
	}
	return m, db, ctx, target, in
}
func TestRubricScoresPersistFrozenEvidenceExactTotalsAndThreeStageDivergence(t *testing.T) {
	m, db, ctx, target, in := rubricScoreFixture(t)
	emitted := []events.Event{}
	m.events = testPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
		emitted = append(emitted, event)
		return 1, nil
	})
	record := func(in RubricScoreInput) RubricScoreReceipt {
		var out RubricScoreReceipt
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.recordRubricScoreOn(ctx, tx, target, in); return e }))
		return out
	}
	internal := in
	internal.Stage = "AGENT_ACCEPTANCE"
	target.ActorKind = "AGENT"
	target.Authority = "verification-task:703"
	agent := record(internal)
	require.True(t, agent.Calculation.Passed)
	require.Equal(t, json.Number("4"), *agent.Calculation.Total)
	require.Len(t, agent.Scores[5].Proofs, 1)
	target.ActorKind = "HUMAN"
	target.Authority = "ROLE:reviewer"
	human := record(in)
	target.ActorKind = "CLIENT"
	target.Authority = "client-package:901:11"
	target.PackageHash = strings.Repeat("d", 64)
	external := in
	external.Stage = "CLIENT_ACCEPTANCE"
	external.Scores = append([]RubricScoreValue(nil), in.Scores...)
	two := json.Number("2")
	external.Scores[0].Score = &two
	client := record(external)
	require.Len(t, emitted, 3)
	require.Equal(t, "rubric.divergence_detected", emitted[2].Type)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, found, err := m.lifecycleCurrentOn(ctx, tx, authn.TenantIDFromContext(ctx), client.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, emitted[2].Type, view.EventType)
		require.Equal(t, view.EventType, view.NoticeType)
		require.NotEmpty(t, view.Reference)
		require.Equal(t, view.ObjectKey, view.Reference, "divergence notification dedupe must identify this exact work/result, not collapse all project scores")
		require.Equal(t, "a", view.Params["dimensions"])
		return nil
	}))
	require.Equal(t, agent.TemplateVersionID, client.TemplateVersionID)
	require.Equal(t, agent.DefinitionHash, client.DefinitionHash)
	var board RubricScoreboard
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; board, e = m.rubricBoardOn(ctx, tx, target, false); return e }))
	require.Len(t, board.Receipts, 3)
	require.Len(t, board.Divergences, 2)
	require.Equal(t, "2", board.Divergences[0].Delta)
	require.Equal(t, "a", board.Divergences[0].Dimension)
	require.Equal(t, "NONE", board.AcceptanceEffect)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; board, e = m.rubricBoardOn(ctx, tx, target, true); return e }))
	for _, r := range board.Receipts {
		require.Zero(t, r.WorkItemID)
		require.Zero(t, r.ResultID)
		require.Empty(t, r.HeadCommit)
		require.Empty(t, r.Authority)
		for _, s := range r.Scores {
			require.Empty(t, s.EvidenceRefs)
			require.Empty(t, s.Proofs)
			if r.ScorerKind != "CLIENT" {
				require.Empty(t, s.Note)
			}
		}
	}
	require.Empty(t, in.Scores[5].Proofs, "command must not mutate caller-owned input")
	_ = human
}
func TestRubricScoresRejectForgedMissingAndUnsupportedProofAndRollback(t *testing.T) {
	for _, fault := range []string{"missing", "foreign_evidence", "spoof_proof", "missing_proof", "failed_proof", "outbox", "actor_kind", "hard_gate_false"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, target, in := rubricScoreFixture(t)
			switch fault {
			case "missing":
				in.Scores = in.Scores[:2]
			case "foreign_evidence":
				in.Scores[0].EvidenceRefs = []guid.ID{999}
			case "spoof_proof":
				in.Scores[0].Proofs = []RubricProofFact{{TaskID: 900}}
			case "missing_proof":
				m.rubricProof = nil
			case "failed_proof":
				m.rubricProof = func(context.Context, *gorm.DB, RubricProofRequest) (RubricProofFact, error) {
					return RubricProofFact{TaskID: 703, Passed: false}, nil
				}
			case "outbox":
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				})
			case "actor_kind":
				target.ActorKind = "AGENT"
			case "hard_gate_false":
				no := false
				in.Scores[5].PassFail = &no
				m.rubricProof = nil
			}
			var out RubricScoreReceipt
			e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.recordRubricScoreOn(ctx, tx, target, in); return e })
			q := query.BindLifecycleReceipts(db, m.names)
			count, ce := q.WithContext(ctx).Where(q.EventType.Eq("rubric.score_recorded")).Count()
			require.NoError(t, ce)
			if fault == "hard_gate_false" {
				require.NoError(t, e)
				require.Nil(t, out.Calculation.Total)
				require.False(t, out.Calculation.Passed)
				require.EqualValues(t, 1, count)
			} else {
				require.Error(t, e)
				require.Zero(t, count)
			}
		})
	}
}
func TestRubricDivergenceUsesExactBoundaryAndLatestStageOnly(t *testing.T) {
	score := func(id guid.ID, stage, n string) RubricScoreReceipt {
		v := json.Number(n)
		return RubricScoreReceipt{ID: id, Stage: stage, Scores: []RubricScoreValue{{RubricDimensionScore: RubricDimensionScore{Key: "q", Score: &v}}}}
	}
	require.Empty(t, rubricDivergences([]RubricScoreReceipt{score(3, "CLIENT_ACCEPTANCE", "3.000000000000000001"), score(2, "AGENT_ACCEPTANCE", "4"), score(1, "CLIENT_ACCEPTANCE", "1")}))
	rows := rubricDivergences([]RubricScoreReceipt{score(3, "CLIENT_ACCEPTANCE", "3"), score(2, "AGENT_ACCEPTANCE", "4")})
	require.Len(t, rows, 1)
	require.Equal(t, "1", rows[0].Delta)
}

func TestRubricClientComparisonIsolatesOtherPublishedPackages(t *testing.T) {
	m, db, ctx, target, input := rubricScoreFixture(t)
	input.Stage = "CLIENT_ACCEPTANCE"
	target.ActorKind = "CLIENT"
	target.PackageHash = strings.Repeat("c", 64)
	record := func(packageID guid.ID, note string) {
		target.PackageID = packageID
		input.Scores[0].Note = note
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordRubricScoreOn(ctx, tx, target, input); return e }))
	}
	record(900, "Current package assessment")
	record(901, "Other recipient private assessment")
	target.PackageID = 900
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		board, e := m.rubricBoardOn(ctx, tx, target, true)
		require.NoError(t, e)
		require.Len(t, board.Receipts, 1)
		require.Equal(t, "Current package assessment", board.Receipts[0].Scores[0].Note)
		all, e := m.rubricBoardOn(ctx, tx, target, false)
		require.NoError(t, e)
		require.Len(t, all.Receipts, 2)
		return nil
	}))
}

func TestRubricScoreStorageAndMissingSourcePreserveReceipts(t *testing.T) {
	m, db, ctx, target, in := rubricScoreFixture(t)
	injected := errors.New("score storage unavailable")
	for _, operation := range []string{"read", "create"} {
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == m.names.LifecycleReceipts.String() {
				tx.AddError(injected)
			}
		}
		if operation == "read" {
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("score-failure", callback))
		} else {
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("score-failure", callback))
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordRubricScoreOn(ctx, tx, target, in); return e }), injected)
		require.NoError(t, db.Callback().Query().Remove("score-failure"))
		require.NoError(t, db.Callback().Create().Remove("score-failure"))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordRubricScoreOn(ctx, tx, target, in); return e }))
	require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Where("event_type=?", "rubric.score_recorded").Update("payload", "invalid").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.rubricBoardOn(ctx, tx, target, false); return e }), ErrConflict)
	m.rubricSubjects.WorkOn = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.rubricSubjectOn(ctx, tx, 99, 700, 0, "", nil); return e }), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.rubricSubjectOn(ctx, tx, 99, 700, 100, "item", nil); return e }), ErrUnavailable)
}

func TestRubricCommandsRejectIncompleteAuthorityAndUnavailableFacts(t *testing.T) {
	for _, fault := range []string{"scores", "stage", "evidence", "binding", "ids", "audit", "proof"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, target, in := rubricScoreFixture(t)
			injected := errors.New("score dependency unavailable")
			want := ErrInvalid
			switch fault {
			case "scores":
				in.Scores = nil
			case "stage":
				in.Stage = "UNKNOWN"
			case "evidence":
				in.Scores[0].EvidenceRefs = nil
			case "binding":
				require.NoError(t, db.Table(m.names.Bindings.String()).Where("project_id=?", 99).Delete(nil).Error)
				want = ErrUnavailable
			case "ids":
				m.ids = failingTemplateIDs{injected}
				want = injected
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
				want = injected
			case "proof":
				m.rubricProof = func(context.Context, *gorm.DB, RubricProofRequest) (RubricProofFact, error) {
					return RubricProofFact{}, injected
				}
				want = injected
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.recordRubricScoreOn(ctx, tx, target, in); return e }), want)
			var count int64
			require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Where("event_type=?", "rubric.score_recorded").Count(&count).Error)
			require.Zero(t, count)
		})
	}
	m, db, ctx, _, in := rubricScoreFixture(t)
	m.rubricSubjects.PackageOn = func(context.Context, *gorm.DB, delivery.RubricClientRequest) (delivery.RubricSubject, error) {
		t.Fatal("invalid result reached package source")
		return delivery.RubricSubject{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.rubricSubjectOn(ctx, tx, 99, 700, 900, "item", &in); return e }), ErrInvalid)
}
