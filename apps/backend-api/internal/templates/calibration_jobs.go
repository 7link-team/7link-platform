package templates

import (
	activityi18n "7link.com/platform/internal/templates/i18n"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_templates_provider_generation_name", DescriptionKey: activityi18n.Namespace + ".activity_job_templates_provider_generation_description", Kind: "templates.provider_generation", Timeout: 11 * time.Minute, MaxAttempts: 1, Run: m.providerGenerationJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_templates_eval_run_name", DescriptionKey: activityi18n.Namespace + ".activity_job_templates_eval_run_description", Kind: "templates.eval_run", Timeout: 11 * time.Minute, MaxAttempts: 1, Run: m.evaluateEvalJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_templates_calibration_evaluate_name", DescriptionKey: activityi18n.Namespace + ".activity_job_templates_calibration_evaluate_description", Kind: "templates.calibration_evaluate", Timeout: 11 * time.Minute, MaxAttempts: 1, Run: m.evaluateCalibrationJob},
		{DisplayNameKey: activityi18n.Namespace + ".activity_job_templates_calibration_review_name", DescriptionKey: activityi18n.Namespace + ".activity_job_templates_calibration_review_description", Kind: "templates.calibration_review", Timeout: time.Minute, MaxAttempts: 1, Run: m.reviewCalibrationJob},
	}
}
func (m *Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{
		{Name: "templates.provider_generation", Kind: "templates.provider_generation", Interval: time.Minute, TenantScope: jobs.ScopePerTenant, Payload: map[string]any{}, Misfire: jobs.MisfireCoalesce},
		{Name: "templates.eval_run", Kind: "templates.eval_run", Interval: time.Minute, TenantScope: jobs.ScopePerTenant, Payload: map[string]any{}, Misfire: jobs.MisfireCoalesce},
		{Name: "templates.calibration_evaluate", Kind: "templates.calibration_evaluate", Interval: time.Minute, TenantScope: jobs.ScopePerTenant, Payload: map[string]any{}, Misfire: jobs.MisfireCoalesce},
		{Name: "templates.calibration_review", Kind: "templates.calibration_review", Interval: time.Hour, TenantScope: jobs.ScopePerTenant, Payload: map[string]any{}, Misfire: jobs.MisfireCoalesce},
	}
}
func (m *Module) evaluateCalibrationJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	var ids []guid.ID
	if err := m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindCalibrationRuns(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.State.Eq("PENDING")).Order(q.ID).Limit(1).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := m.evaluateCalibrationRun(ctx, job.TenantID, id); err != nil {
			return err
		}
	}
	return nil
}

// No network work holds a database lock. The second transaction compares the
// frozen run pin again, so a stale evaluator cannot overwrite a newer attempt.
func (m *Module) evaluateCalibrationRun(ctx context.Context, tenant, id guid.ID) error {
	var head *model.Calibration
	var run *model.CalibrationRun
	var snapshot calibrationSnapshot
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindCalibrationRuns(tx, m.names)
		var err error
		run, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return err
		}
		if run.State != "PENDING" {
			return nil
		}
		c := query.BindCalibrations(tx, m.names)
		head, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(run.CalibrationID), c.CurrentRunID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(run.Snapshot))
		if hex.EncodeToString(digest[:]) != run.SHA256 || json.Unmarshal([]byte(run.Snapshot), &snapshot) != nil {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return err
	}
	if run.State != "PENDING" {
		return nil
	}
	request := CalibrationEvaluationRequest{TenantID: tenant, RunID: id, TemplateVersionID: head.TemplateVersionID, DefinitionSHA256: snapshot.DefinitionSHA256, Verifier: snapshot.Verifier, ProviderRef: head.ProviderRef, MachineID: head.MachineID, Samples: []CalibrationInput{}}
	request.InitiatorID = head.CreatedBy
	if snapshot.InitiatorID > 0 {
		request.InitiatorID = snapshot.InitiatorID
	}
	for _, s := range snapshot.Samples {
		request.Samples = append(request.Samples, CalibrationInput{SampleID: s.ID, Input: slices.Clone(s.Input), SHA256: s.SHA256})
	}
	state := "UNAVAILABLE"
	limitations := "Actual calibration evaluator is unavailable; agreement is unknown"
	var evaluation CalibrationEvaluation
	var agreement *int
	if m.calibrationEvaluator != nil {
		work, cancel := context.WithTimeout(ctx, 10*time.Minute)
		evaluation, err = m.calibrationEvaluator(work, request)
		cancel()
		if err == nil {
			var score int
			score, err = calibrationAgreement(snapshot, evaluation, head.ProviderRef, head.MachineID)
			if err == nil {
				state = "COMPLETED"
				limitations = "Accuracy over frozen Human labels; future performance remains unproven"
				agreement = &score
			} else {
				state = "FAILED"
				limitations = "Evaluator response lacks complete, uniquely attributed verdicts and evidence"
			}
		} else {
			limitations = "Actual evaluator failed or was unavailable; agreement is unknown"
		}
	}
	return m.transaction(ctx, func(tx *gorm.DB) error {
		// All mutations acquire the pinned version before calibration/run rows.
		v := query.BindVersions(tx, m.names)
		version, e := v.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(v.TenantID.Eq(tenant), v.ID.Eq(head.TemplateVersionID)).First()
		if e != nil {
			return e
		}
		if version.SHA256 != snapshot.DefinitionSHA256 {
			return ErrConflict
		}
		c := query.BindCalibrations(tx, m.names)
		row, e := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ID.Eq(head.ID)).First()
		if e != nil {
			return e
		}
		if row.CurrentRunID != id {
			return ErrConflict
		}
		q := query.BindCalibrationRuns(tx, m.names)
		current, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if e != nil {
			return e
		}
		if current.State != "PENDING" {
			return nil
		}
		now := time.Now().UTC()
		results := []CalibrationResult{}
		if state == "COMPLETED" {
			results = evaluation.Results
		}
		raw, e := json.Marshal(results)
		if e != nil {
			return e
		}
		update := tx.WithContext(ctx).Table(m.names.CalibrationRuns.String()).Where("tenant_id=? AND id=? AND state='PENDING'", tenant, id).Updates(map[string]any{"state": state, "results": string(raw), "agreement_ppm": agreement, "completed_at": now, "limitations": limitations})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrConflict
		}
		prior := row.State
		if agreement == nil {
			row.State = "UNCALIBRATED"
			if run.Recheck && (prior == "CALIBRATED" || prior == "DRIFTED") {
				row.State = "DRIFTED"
			}
		} else if *agreement < row.TargetPPM {
			row.State = "UNCALIBRATED"
			if run.Recheck && (prior == "CALIBRATED" || prior == "DRIFTED") {
				row.State = "DRIFTED"
			}
		} else if row.Method == "PROBE" {
			row.State = "CALIBRATED"
			row.CalibratedAt = &now
			row.CalibratedBy = nil
		} else if !(run.Recheck && prior == "CALIBRATED") {
			row.State = "CALIBRATING"
		}
		if row.State != "CALIBRATED" {
			row.CalibratedAt = nil
			row.CalibratedBy = nil
		}
		row.DriftCheckedAt = &now
		interval, e := calibrationParameter(snapshot.Verifier, "calibration_interval_days", 30, 3650)
		if e != nil {
			return e
		}
		row.NextReviewAt = now.Add(time.Duration(interval) * 24 * time.Hour)
		row.Version++
		if e = m.saveCalibrationOn(ctx, tx, row); e != nil {
			return e
		}
		name := "template.calibration_evaluated"
		if row.State == "DRIFTED" && prior != "DRIFTED" {
			name = "template.calibration_drifted"
		}
		return m.calibrationEventOn(ctx, tx, tenant, row.ID, name, "SYSTEM", 0, map[string]any{"run_id": id, "run_state": state, "state": row.State, "agreement_ppm": agreement, "snapshot_sha256": run.SHA256})
	})
}
func calibrationAgreement(snapshot calibrationSnapshot, evaluation CalibrationEvaluation, provider string, machine guid.ID) (int, error) {
	if evaluation.ProviderRef != provider || evaluation.MachineID != machine || len(evaluation.Results) != len(snapshot.Samples) || len(snapshot.Samples) == 0 {
		return 0, ErrInvalid
	}
	expected := map[guid.ID]string{}
	for _, s := range snapshot.Samples {
		if expected[s.ID] != "" {
			return 0, ErrInvalid
		}
		expected[s.ID] = s.Expected
	}
	seen := map[guid.ID]bool{}
	matched := 0
	for _, r := range evaluation.Results {
		if expected[r.SampleID] == "" || seen[r.SampleID] || !slices.Contains([]string{"PASS", "WARN", "FAIL", "INCONCLUSIVE"}, r.Verdict) || len(r.EvidenceRefs) == 0 || len(r.EvidenceRefs) > 20 {
			return 0, ErrInvalid
		}
		seen[r.SampleID] = true
		for _, ref := range r.EvidenceRefs {
			if strings.TrimSpace(ref) == "" || len(ref) > 2000 {
				return 0, ErrInvalid
			}
		}
		if r.Verdict == expected[r.SampleID] {
			matched++
		}
	}
	return matched * 1000000 / len(snapshot.Samples), nil
}
func (m *Module) reviewCalibrationJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	return m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindCalibrations(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID)).Order(q.ID).Limit(1001).Find()
		if err != nil {
			return err
		}
		if len(rows) > 1000 {
			return ErrUnavailable
		}
		for _, hint := range rows {
			// Version locks serialize Human confirmation, new samples and review.
			v := query.BindVersions(tx, m.names)
			version, e := v.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(v.TenantID.Eq(job.TenantID), v.ID.Eq(hint.TemplateVersionID)).First()
			if e != nil {
				return e
			}
			row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(hint.ID)).First()
			if e != nil {
				return e
			}
			r := query.BindCalibrationRuns(tx, m.names)
			run, e := r.WithContext(ctx).Where(r.TenantID.Eq(job.TenantID), r.ID.Eq(row.CurrentRunID)).First()
			if e != nil {
				return e
			}
			if run.State == "PENDING" {
				continue
			}
			s := query.BindCalibrationSamples(tx, m.names)
			escapes, e := s.WithContext(ctx).Where(s.TenantID.Eq(job.TenantID), s.TemplateVersionID.Eq(row.TemplateVersionID), s.VerifierKey.Eq(row.VerifierKey), s.Origin.Eq("ESCAPE")).Count()
			if e != nil {
				return e
			}
			if row.NextReviewAt.After(time.Now().UTC()) && escapes-int64(run.EscapeCount) < 5 {
				continue
			}
			verifier, e := calibrationVerifier(version, row.VerifierKey)
			if e != nil {
				return e
			}
			if e = m.newCalibrationRunOn(ctx, tx, row, version, verifier, true); e != nil {
				return e
			}
		}
		return nil
	})
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
