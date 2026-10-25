package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/templates/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCalibrationInstalledModelHTTPHumanAcceptanceAndActualDrift(t *testing.T) {
	fault := ""
	m, db, ctx, in := installedVerifierFixture(t, &fault, "MODEL_SCORED")
	start := &calibrationStartInput{IdempotencyKey: "installed-model-start", Body: in}
	first, e := m.calibrationStartHTTP(ctx, start)
	require.NoError(t, e)
	replay, e := m.calibrationStartHTTP(ctx, start)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	head := first.Body.Data
	require.Equal(t, "PENDING", head.RunState)
	require.NoError(t, m.evaluateCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	head = calibrationGetForTest(t, m, db, ctx, head.ID)
	require.Equal(t, "CALIBRATING", head.State)
	require.Equal(t, "COMPLETED", head.RunState)
	require.Len(t, head.Results, 20)
	require.Equal(t, 1000000, *head.AgreementPPM)
	read := func() string {
		var state string
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, s, e := m.verifierGetOn(ctx, tx, 1, in.TemplateVersionID, in.VerifierKey)
			state = s
			return e
		}))
		return state
	}
	require.Equal(t, "CALIBRATING", read(), "a model cannot accept its own calibration")
	confirm := &calibrationConfirmInput{ID: head.ID.String(), IfMatch: fmt.Sprintf(`"%d"`, head.Version), IdempotencyKey: "installed-model-accept"}
	confirm.Body.Accept = true
	accepted, e := m.calibrationConfirmHTTP(ctx, confirm)
	require.NoError(t, e)
	repeated, e := m.calibrationConfirmHTTP(ctx, confirm)
	require.NoError(t, e)
	require.Equal(t, accepted.Body.Data, repeated.Body.Data)
	head = accepted.Body.Data
	require.Equal(t, "CALIBRATED", head.State)
	require.Equal(t, guid.ID(11), *head.CalibratedBy)
	require.Equal(t, "CALIBRATED", read())
	original := head
	require.NoError(t, db.Table(m.names.Calibrations.String()).Where("tenant_id=? AND id=?", 1, head.ID).Update("next_review_at", time.Now().UTC().Add(-time.Hour)).Error)
	fault = "invert"
	require.NoError(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, m.evaluateCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	head = calibrationGetForTest(t, m, db, ctx, head.ID)
	require.Equal(t, "DRIFTED", head.State)
	require.Equal(t, 0, *head.AgreementPPM)
	require.Nil(t, head.CalibratedBy)
	require.Equal(t, "DRIFTED", read())
	require.NotEqual(t, original.RunID, head.RunID)
	var historical string
	require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Select("state").Where("id=?", original.RunID).Scan(&historical).Error)
	require.Equal(t, "COMPLETED", historical)
}
func TestCalibrationInstalledModelRejectsWrongModelIdentity(t *testing.T) {
	fault := "model"
	m, db, ctx, in := installedVerifierFixture(t, &fault, "MODEL_SCORED")
	head := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
	head = calibrationGetForTest(t, m, db, ctx, head.ID)
	require.Equal(t, "UNAVAILABLE", head.RunState)
	require.Nil(t, head.AgreementPPM)
	require.Equal(t, "UNCALIBRATED", head.State)
}

func TestCalibrationReviewSchedulesDriftOnDueDateAndFiveEscapeLabels(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	m.calibrationEvaluator = calibrationEvaluatorForTest(false)
	view := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	view = calibrationGetForTest(t, m, db, ctx, view.ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		view, e = m.confirmCalibrationOn(ctx, tx, view.ID, view.Version, true, "")
		return e
	}))
	for i := 20; i < 25; i++ {
		expected := "PASS"
		if i%2 != 0 {
			expected = "FAIL"
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.addCalibrationSampleOn(ctx, tx, CalibrationSampleDraft{TemplateVersionID: in.TemplateVersionID, VerifierKey: in.VerifierKey, Input: json.RawMessage(fmt.Sprintf(`{"sample":%d}`, i)), Expected: expected}, "ESCAPE")
			return e
		}))
	}
	require.NoError(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	recheck := calibrationGetForTest(t, m, db, ctx, view.ID)
	require.NotEqual(t, view.RunID, recheck.RunID)
	require.Equal(t, "PENDING", recheck.RunState)
	require.Equal(t, 25, recheck.SampleSize)
	m.calibrationEvaluator = calibrationEvaluatorForTest(true)
	require.NoError(t, m.evaluateCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	drift := calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, "DRIFTED", drift.State)
	require.Equal(t, 0, *drift.AgreementPPM)
	require.Nil(t, drift.CalibratedAt)
	require.NoError(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	require.Equal(t, drift.RunID, calibrationGetForTest(t, m, db, ctx, view.ID).RunID, "five already-observed escapes cannot repeatedly trigger review")
	var original model.CalibrationRun
	require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Where("tenant_id=1 AND id=?", view.RunID).First(&original).Error)
	require.Equal(t, "COMPLETED", original.State)
	require.Equal(t, 1000000, *original.AgreementPPM, "drift cannot rewrite the original accepted evidence")
	require.NoError(t, db.Table(m.names.Calibrations.String()).Where("tenant_id=1 AND id=?", view.ID).Update("next_review_at", time.Now().UTC().Add(-time.Minute)).Error)
	require.NoError(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	require.NotEqual(t, drift.RunID, calibrationGetForTest(t, m, db, ctx, view.ID).RunID, "due periodic review creates a new frozen run")
}
func TestCalibrationProbeNeedsBothControlsAndExactActualMachineEvidence(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "DETERMINISTIC", 2)
	m.calibrationEvaluator = calibrationEvaluatorForTest(false)
	view := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, view.RunID))
	view = calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, "CALIBRATED", view.State)
	require.Equal(t, 1000000, *view.AgreementPPM)
	require.Nil(t, view.CalibratedBy, "deterministic probes do not invent a Human approval")
	m.calibrationEvaluator = nil
	require.NoError(t, db.Table(m.names.Calibrations.String()).Where("tenant_id=1 AND id=?", view.ID).Update("next_review_at", time.Now().UTC().Add(-time.Minute)).Error)
	require.NoError(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}))
	unavailable := calibrationGetForTest(t, m, db, ctx, view.ID)
	require.Equal(t, "UNAVAILABLE", unavailable.RunState)
	require.Nil(t, unavailable.AgreementPPM)
	require.Equal(t, "DRIFTED", unavailable.State, "expired probe evidence cannot continue authoritative decisions without recheck")
}
func TestCalibrationAgreementRejectsDuplicateForeignOrMissingObservations(t *testing.T) {
	snapshot := calibrationSnapshot{Samples: []CalibrationSampleView{{ID: 1, Expected: "PASS"}, {ID: 2, Expected: "FAIL"}}}
	valid := CalibrationEvaluation{ProviderRef: "source:v1", MachineID: 10, Results: []CalibrationResult{{SampleID: 1, Verdict: "PASS", EvidenceRefs: []string{"source:1"}}, {SampleID: 2, Verdict: "FAIL", EvidenceRefs: []string{"source:2"}}}}
	for _, change := range []func(*CalibrationEvaluation){
		func(e *CalibrationEvaluation) { e.Results = e.Results[:1] },
		func(e *CalibrationEvaluation) { e.Results[1].SampleID = 1 },
		func(e *CalibrationEvaluation) { e.Results[1].SampleID = 3 },
		func(e *CalibrationEvaluation) { e.Results[1].EvidenceRefs = nil },
		func(e *CalibrationEvaluation) { e.ProviderRef = "other:v1" },
		func(e *CalibrationEvaluation) { e.MachineID = 11 },
	} {
		test := valid
		test.Results = append([]CalibrationResult(nil), valid.Results...)
		change(&test)
		_, e := calibrationAgreement(snapshot, test, "source:v1", 10)
		require.ErrorIs(t, e, ErrInvalid)
	}
	actual, e := calibrationAgreement(snapshot, valid, "source:v1", 10)
	require.NoError(t, e)
	require.Equal(t, 1000000, actual)
}

func TestCalibrationJobsRejectCorruptPinsAndRollbackFinalization(t *testing.T) {
	for _, failure := range []string{"snapshot", "head-pin", "read", "update"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
			m.calibrationEvaluator = calibrationEvaluatorForTest(false)
			head := calibrationStartForTest(t, m, db, ctx, in)
			injected := errors.New("calibration worker storage unavailable")
			switch failure {
			case "snapshot":
				require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Where("id=?", head.RunID).Update("snapshot", "{}").Error)
			case "head-pin":
				require.NoError(t, db.Table(m.names.Calibrations.String()).Where("id=?", head.ID).Update("current_run_id", 0).Error)
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("calibration-job-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.CalibrationRuns.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Query().Remove("calibration-job-failure")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("calibration-job-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.CalibrationRuns.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Update().Remove("calibration-job-failure")
			}
			e := m.evaluateCalibrationJob(ctx, jobs.Job{TenantID: 1})
			require.Error(t, e)
			if failure == "read" || failure == "update" {
				require.ErrorIs(t, e, injected)
			} else {
				require.ErrorIs(t, e, ErrConflict)
			}
			if failure == "read" {
				require.NoError(t, db.Callback().Query().Remove("calibration-job-failure"))
			}
			var state string
			require.NoError(t, db.Table(m.names.CalibrationRuns.String()).Select("state").Where("id=?", head.RunID).Scan(&state).Error)
			require.Equal(t, "PENDING", state)
		})
	}
	m, _, ctx, _ := calibrationFixture(t, "MODEL_SCORED", 0)
	require.ErrorIs(t, m.evaluateCalibrationJob(ctx, jobs.Job{}), ErrInvalid)
	require.ErrorIs(t, m.reviewCalibrationJob(ctx, jobs.Job{}), ErrInvalid)
}

func TestCalibrationReviewCurrentSourceFailureAndJobDeclarations(t *testing.T) {
	m, db, ctx, in := calibrationFixture(t, "MODEL_SCORED", 20)
	m.calibrationEvaluator = calibrationEvaluatorForTest(false)
	head := calibrationStartForTest(t, m, db, ctx, in)
	require.NoError(t, m.evaluateCalibrationRun(ctx, 1, head.RunID))
	head = calibrationGetForTest(t, m, db, ctx, head.ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.confirmCalibrationOn(ctx, tx, head.ID, head.Version, true, "Human confirms")
		return e
	}))
	require.NoError(t, db.Table(m.names.Calibrations.String()).Where("id=?", head.ID).Update("next_review_at", time.Now().Add(-time.Hour)).Error)
	injected := errors.New("review current source unavailable")
	for _, table := range []string{m.names.Calibrations.String(), m.names.Versions.String(), m.names.CalibrationRuns.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("review-source-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, m.reviewCalibrationJob(ctx, jobs.Job{TenantID: 1}), injected)
		require.NoError(t, db.Callback().Query().Remove("review-source-failure"))
	}
	handlers, schedules := m.JobHandlers(), m.JobSchedules()
	require.Len(t, handlers, 4)
	require.Len(t, schedules, 4)
	for i, h := range handlers {
		require.Equal(t, h.Kind, schedules[i].Kind)
		require.NotNil(t, h.Run)
		require.Equal(t, 1, h.MaxAttempts)
		require.Positive(t, h.Timeout)
		require.Equal(t, jobs.ScopePerTenant, schedules[i].TenantScope)
	}
}
