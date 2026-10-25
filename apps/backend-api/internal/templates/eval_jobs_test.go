package templates

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestEvalActualEvaluatorCannotSeeLabelsAndTerminalResultsRemainImmutable(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	created := addEvalFixture(t, m, db, ctx, draft)
	calls := 0
	m.calibrationEvaluator = func(_ context.Context, r CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
		calls++
		require.Len(t, r.Samples, 1)
		require.NotContains(t, string(r.Samples[0].Input), "expected")
		require.NotContains(t, string(r.Samples[0].Input), "PASS")
		require.Len(t, r.Samples[0].SHA256, 64)
		return CalibrationEvaluation{ProviderRef: r.ProviderRef, MachineID: r.MachineID, Results: []CalibrationResult{{SampleID: created.ID, Verdict: "PASS", EvidenceRefs: []string{"immutable:execution/42"}}}}, nil
	}
	var first EvalRunView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual/provider"}})
		return e
	}))
	require.Equal(t, "RUNNING", first.State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual/provider"}})
		return e
	}), ErrConflict)
	require.NoError(t, m.evaluateEvalRun(ctx, 1, first.ID))
	require.NoError(t, m.evaluateEvalRun(ctx, 1, first.ID))
	require.Equal(t, 1, calls)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.evalRunOn(ctx, tx, first.ID)
		require.Equal(t, "COMPLETED", out.State)
		require.Equal(t, 1, out.Passed)
		require.Zero(t, out.Failed)
		require.Equal(t, 1000000, *out.AgreementPPM)
		require.Equal(t, first.SHA256, out.SHA256)
		return e
	}))
}
func TestEvalRejectsUnprovenEvaluatorVerdicts(t *testing.T) {
	for _, refs := range [][]string{nil, {""}, {strings.Repeat("x", 2001)}} {
		t.Run("invalid-reference", func(t *testing.T) {
			m, db, ctx, v, draft := evalFixture(t)
			created := addEvalFixture(t, m, db, ctx, draft)
			m.calibrationEvaluator = func(_ context.Context, r CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
				return CalibrationEvaluation{ProviderRef: r.ProviderRef, Results: []CalibrationResult{{SampleID: created.ID, Verdict: "PASS", EvidenceRefs: refs}}}, nil
			}
			var run EvalRunView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				run, e = m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
				return e
			}))
			require.NoError(t, m.evaluateEvalRun(ctx, 1, run.ID))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				read, e := m.evalRunOn(ctx, tx, run.ID)
				require.Equal(t, "FAILED", read.State)
				require.Nil(t, read.AgreementPPM)
				require.Empty(t, read.Results)
				return e
			}))
		})
	}
}

func TestEvalJobRecoversEscapeAndRetainsUnknownVerdicts(t *testing.T) {
	m, db, ctx, v, draft := evalFixture(t)
	addEvalFixture(t, m, db, ctx, draft)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.proposeEscapeEvalOn(ctx, tx, governance.EscapeEvalFact{TenantID: 1, ProjectID: 99, ID: 901, WorkItemID: 700, Title: "Human rejection", Snapshot: []byte(`{"decision":"REJECTED"}`)})
		return e
	}))
	require.ErrorIs(t, m.evaluateEvalJob(ctx, jobs.Job{}), ErrInvalid)
	require.NoError(t, m.evaluateEvalJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, m.evaluateEvalJob(ctx, jobs.Job{TenantID: 1}))
	m.calibrationEvaluator = func(_ context.Context, r CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
		out := CalibrationEvaluation{ProviderRef: r.ProviderRef, MachineID: r.MachineID}
		for _, sample := range r.Samples {
			out.Results = append(out.Results, CalibrationResult{SampleID: sample.SampleID, Verdict: "INCONCLUSIVE", EvidenceRefs: []string{"actual-observation"}})
		}
		return out, nil
	}
	var run EvalRunView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, e = m.startEvalOn(ctx, tx, EvalStart{TemplateVersionID: v.ID, TemplateKey: v.Key, Target: EvalTarget{VerifierKey: "unit-tests", ProviderRef: "actual"}})
		return e
	}))
	require.NoError(t, m.evaluateEvalJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.evalRunOn(ctx, tx, run.ID)
		require.Equal(t, "COMPLETED", out.State)
		require.Equal(t, 2, out.Inconclusive)
		require.Nil(t, out.AgreementPPM)
		return e
	}))
	injected := errors.New("eval scan unavailable")
	for _, table := range []string{m.names.EvalCases.String(), m.names.EvalRuns.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("eval-scan-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, m.evaluateEvalJob(ctx, jobs.Job{TenantID: 1}), injected)
		require.NoError(t, db.Callback().Query().Remove("eval-scan-failure"))
	}
	require.ErrorIs(t, m.evaluateEvalRun(ctx, 1, guid.ID(999)), gorm.ErrRecordNotFound)
}
