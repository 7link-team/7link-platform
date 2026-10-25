package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func calibrationFixture(t *testing.T, kind string, count int) (*Module, *gorm.DB, context.Context, CalibrationStart) {
	t.Helper()
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	runner := "AGENT_JUDGE"
	argv := []string(nil)
	method := "AGREEMENT"
	machine := guid.ID(0)
	if kind == "DETERMINISTIC" {
		runner = "VISUAL"
		argv = []string{"probe"}
		method = "PROBE"
		machine = 101
	}
	d.Metadata = map[string]any{"verifiers": []VerifierDefinition{{Key: "visual", Kind: kind, Runner: runner, Version: 1, Argv: argv, TimeoutSec: 60, Limitations: "Evidence is limited to frozen sample inputs"}}}
	v := publishRuntime(t, m, db, ctx, "calibration", d)
	for i := 0; i < count; i++ {
		expected := "PASS"
		if i%2 != 0 {
			expected = "FAIL"
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: v.ID, VerifierKey: "visual", Input: json.RawMessage(fmt.Sprintf(`{"sample":%d}`, i)), Expected: expected}, "MANUAL")
			return e
		}))
	}
	return m, db, ctx, CalibrationStart{TemplateVersionID: v.ID, VerifierKey: "visual", ProviderRef: "actual-source:v1", Method: method, MachineID: machine}
}
func calibrationStartForTest(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, in CalibrationStart) CalibrationView {
	t.Helper()
	var out CalibrationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.startCalibrationOn(ctx, tx, in); return e }))
	return out
}
func calibrationGetForTest(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID) CalibrationView {
	t.Helper()
	var out CalibrationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, e := m.calibrationOn(ctx, tx, id, "template_read")
		if e != nil {
			return e
		}
		out, e = m.calibrationViewOn(ctx, tx, row)
		return e
	}))
	return out
}
func calibrationEvaluatorForTest(fail bool) CalibrationEvaluator {
	return func(_ context.Context, in CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
		out := CalibrationEvaluation{ProviderRef: in.ProviderRef, MachineID: in.MachineID}
		for _, sample := range in.Samples {
			var input struct {
				Sample int `json:"sample"`
			}
			if e := json.Unmarshal(sample.Input, &input); e != nil {
				return out, e
			}
			verdict := "PASS"
			if input.Sample%2 != 0 {
				verdict = "FAIL"
			}
			if fail {
				if verdict == "PASS" {
					verdict = "FAIL"
				} else {
					verdict = "PASS"
				}
			}
			out.Results = append(out.Results, CalibrationResult{SampleID: sample.SampleID, Verdict: verdict, EvidenceRefs: []string{"fixture-observation:" + sample.SampleID.String()}})
		}
		return out, nil
	}
}
func TestCalibrationRequiresDistinctHumanLabelsAndActualEvaluator(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 19)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), ErrCalibrationSamples)
	q := query.BindCalibrations(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n, "insufficient labels rollback the aggregate")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{"sample":0}`), Expected: "FAIL"}, "MANUAL")
		return e
	}), ErrConflict, "conflicting labels cannot inflate sample count")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{"sample":19}`), Expected: "FAIL"}, "MANUAL")
		return e
	}))
	view := calibrationStartForTest(t, m, db, ctx, in)
	require.Equal(t, "UNAVAILABLE", view.RunState)
	require.Equal(t, "UNCALIBRATED", view.State)
	require.Nil(t, view.AgreementPPM)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, true, "")
		return e
	}), ErrCalibrationPending)
	services := module.NewServices()
	require.NoError(t, m.provideCalibration(services))
	require.NoError(t, services.Seal())
	connector, e := module.Resolve[CalibrationEvaluatorConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalid)
	require.NoError(t, connector.Bind(calibrationEvaluatorForTest(false)))
	require.ErrorIs(t, connector.Bind(calibrationEvaluatorForTest(false)), ErrInvalid)
}
func TestCalibrationHumanAcceptanceCurrentAuthorityAndAtomicity(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	m.calibrationEvaluator = calibrationEvaluatorForTest(false)
	view := calibrationStartForTest(t, m, db, ctx, in)
	require.Equal(t, "PENDING", view.RunState)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), ErrConflict)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, view.RunID))
	view = calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, "COMPLETED", view.RunState)
	require.Equal(t, 1000000, *view.AgreementPPM)
	require.Equal(t, "CALIBRATING", view.State, "actual model agreement alone does not imply Human acceptance")
	require.Len(t, view.Results, 20)
	fail := errors.New("audit failed")
	oldAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fail }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, true, "")
		return e
	}), fail)
	require.Equal(t, view, calibrationGetForTest(t, m, db, ctx, view.ID), "audit failure rolls back acceptance")
	m.audit = oldAudit
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		view, e = m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, true, "")
		return e
	}))
	require.Equal(t, "CALIBRATED", view.State)
	require.Equal(t, guid.ID(11), *view.CalibratedBy)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, view.ID, view.Version-1, true, "")
		return e
	}), ErrConflict)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.calibrationOn(other, tx, view.ID, "template_read"); return e }), ErrNotFound)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, false, "")
		return e
	}), ErrForbidden)
}
func TestCalibrationNeverAcceptsBelowTargetOrIncompleteEvidence(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	m.calibrationEvaluator = calibrationEvaluatorForTest(true)
	view := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, view.RunID))
	view = calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, 0, *view.AgreementPPM)
	require.Equal(t, "UNCALIBRATED", view.State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, true, "")
		return e
	}), ErrCalibrationTarget)
	m.calibrationEvaluator = func(ctx context.Context, in CalibrationEvaluationRequest) (CalibrationEvaluation, error) {
		out, e := calibrationEvaluatorForTest(false)(ctx, in)
		out.Results[0].EvidenceRefs = nil
		return out, e
	}
	view = calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, view.RunID))
	view = calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, "FAILED", view.RunState)
	require.Nil(t, view.AgreementPPM)
}

func TestCalibrationCommandsRollbackStorageAndRejectInvalidSources(t *testing.T) {
	for _, failure := range []string{"sample-create", "head-create", "run-create", "head-update", "sample-read"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
			injected := errors.New("calibration persistence unavailable")
			table := m.names.Calibrations.String()
			operation := "create"
			switch failure {
			case "sample-create":
				table = m.names.CalibrationSamples.String()
			case "run-create":
				table = m.names.CalibrationRuns.String()
			case "head-update":
				operation = "update"
			case "sample-read":
				operation = "query"
				table = m.names.CalibrationSamples.String()
			}
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(injected)
				}
			}
			switch operation {
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("calibration-failure", callback))
				defer db.Callback().Create().Remove("calibration-failure")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("calibration-failure", callback))
				defer db.Callback().Update().Remove("calibration-failure")
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("calibration-failure", callback))
				defer db.Callback().Query().Remove("calibration-failure")
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if failure == "sample-create" {
					_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{"sample":99}`), Expected: "PASS"}, "MANUAL")
					return e
				}
				_, e := m.startCalibrationOn(ctx, tx, in)
				return e
			})
			require.ErrorIs(t, err, injected)
			if operation == "query" {
				require.NoError(t, db.Callback().Query().Remove("calibration-failure"))
			}
			var n int64
			require.NoError(t, db.Table(m.names.Calibrations.String()).Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, db.Table(m.names.CalibrationSamples.String()).Count(&n).Error)
			require.EqualValues(t, 20, n)
		})
	}
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 0)
	for _, draft := range []CalibrationSampleDraft{{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{}`), Expected: "OTHER"}, {TemplateVersionID: in.TemplateVersionID, VerifierKey: "missing", Input: json.RawMessage(`{}`), Expected: "PASS"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.addCalibrationSampleOn(ctx, tx, draft, "MANUAL"); return e }), ErrInvalid)
	}
	in.ProviderRef = " leading"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), ErrInvalid)
	in.ProviderRef = "source"
	in.Method = "PROBE"
	in.MachineID = 101
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), ErrInvalid)
	for _, value := range []any{"bad", 0, 101} {
		_, e := calibrationParameter(VerifierDefinition{Params: map[string]any{"limit": value}}, "limit", 20, 100)
		require.ErrorIs(t, e, ErrInvalid)
	}
}

func TestCalibrationRequiresPublishedVersionAndAvailableCurrentDependencies(t *testing.T) {
	m, db, ctx := templateHarness(t)
	draft := draftTemplate(t, m, db, ctx, "draft-calibration")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.startCalibrationOn(ctx, tx, CalibrationStart{TemplateVersionID: draft.ID, VerifierKey: "visual", ProviderRef: "source", Method: "AGREEMENT"})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: draft.ID, VerifierKey: "visual", Input: json.RawMessage(`{}`), Expected: "PASS"}, "MANUAL")
		return e
	}), ErrConflict)
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{"sample":99}`), Expected: "PASS"}, "MANUAL")
		return e
	}), ErrUnavailable)
	n, e := calibrationParameter(VerifierDefinition{Params: map[string]any{"limit": 12}}, "limit", 20, 100)
	require.NoError(t, e)
	require.Equal(t, 12, n)
}

func TestCalibrationIDSourceFailureRollsBackSamplesAndHead(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	injected := errors.New("identifier unavailable")
	m.ids = failingTemplateIDs{injected}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(`{"sample":99}`), Expected: "PASS"}, "MANUAL")
		return e
	}), injected)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startCalibrationOn(ctx, tx, in); return e }), injected)
	q := query.BindCalibrations(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
}
