package templates

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) evaluateEvalJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	// Recover an escape arriving while its prior target was RUNNING. The
	// snapshot itself is the durable inclusion marker; retries add no duplicate.
	var escapes []model.EvalCase
	if err := m.transaction(ctx, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND origin=? AND active=?", job.TenantID, "ESCAPE", true).Order("id").Limit(1001).Find(&escapes).Error
	}); err != nil {
		return err
	}
	if len(escapes) > 1000 {
		return ErrUnavailable
	}
	for _, escape := range escapes {
		if err := m.transaction(ctx, func(tx *gorm.DB) error { return m.enqueueEscapeEvaluationsOn(ctx, tx, &escape) }); err != nil {
			return err
		}
	}
	var runs []model.EvalRun
	if err := m.transaction(ctx, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND state=?", job.TenantID, "RUNNING").Order("id").Limit(1).Find(&runs).Error
	}); err != nil {
		return err
	}
	for _, run := range runs {
		if err := m.evaluateEvalRun(ctx, job.TenantID, run.ID); err != nil {
			return err
		}
	}
	return nil
}

// Escape learning reuses only targets previously configured by a Human for
// this same project. It does not invent a provider or extend project visibility.
func (m *Module) enqueueEscapeEvaluationsOn(ctx context.Context, tx *gorm.DB, escape *model.EvalCase) error {
	q := query.BindVersions(tx, m.names)
	if _, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(escape.TenantID), q.Key.Eq(escape.TemplateKey)).Order(q.ID).First(); err != nil {
		return err
	}
	var runs []model.EvalRun
	if err := tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND template_key=?", escape.TenantID, escape.TemplateKey).Order("id DESC").Limit(1001).Find(&runs).Error; err != nil {
		return err
	}
	if len(runs) > 1000 {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for _, previous := range runs {
		if seen[previous.TargetKey] {
			continue
		}
		seen[previous.TargetKey] = true
		view, err := evalRunView(&previous)
		if err != nil {
			return err
		}
		sameProject, alreadyIncluded := false, false
		for _, c := range view.Snapshot.Cases {
			if c.ProjectID == escape.ProjectID {
				sameProject = true
			}
			if c.ID == escape.ID {
				alreadyIncluded = true
			}
		}
		if !sameProject || alreadyIncluded || previous.State == "RUNNING" {
			continue
		}
		// Stay on the exact previously configured template version and verifier.
		// Include current active cases only within its existing project scope.
		allowed := map[guid.ID]bool{}
		for _, c := range view.Snapshot.Cases {
			allowed[c.ProjectID] = true
		}
		var cases []model.EvalCase
		if err = tx.WithContext(ctx).Table(m.names.EvalCases.String()).Where("tenant_id=? AND template_key=? AND active=?", escape.TenantID, escape.TemplateKey, true).Order("id").Limit(1001).Find(&cases).Error; err != nil {
			return err
		}
		if len(cases) > 1000 {
			return ErrUnavailable
		}
		snapshot := view.Snapshot
		snapshot.Cases = []EvalCaseView{}
		for _, c := range cases {
			if !allowed[c.ProjectID] {
				continue
			}
			v, e := evalCaseView(&c)
			if e != nil {
				return e
			}
			snapshot.Cases = append(snapshot.Cases, v)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		run := &model.EvalRun{CreatedBy: previous.CreatedBy, TenantID: escape.TenantID, ID: guid.ID(id), TemplateVersionID: previous.TemplateVersionID, TemplateKey: escape.TemplateKey, TargetKey: previous.TargetKey, Snapshot: string(raw), SHA256: evalDigest(raw), State: "RUNNING", Results: "[]", CasesTotal: len(snapshot.Cases), Inconclusive: len(snapshot.Cases), StartedAt: now, Limitations: "Escape-triggered evaluation of the configured target; unlabelled cases remain unknown"}
		if m.calibrationEvaluator == nil {
			run.State = "UNAVAILABLE"
			run.FinishedAt = &now
			run.Limitations = "Actual evaluator unavailable; escape retained and agreement unknown"
		}
		if err = tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Create(run).Error; err != nil {
			return err
		}
		if err = m.evalEventOn(ctx, tx, run.TenantID, escape.ProjectID, run.ID, "eval.run_changed", true, map[string]any{"source_escape_id": escape.SourceEscapeID, "state": run.State, "snapshot_sha256": run.SHA256}); err != nil {
			return err
		}
	}
	return nil
}

// Evaluation occurs without database locks. The frozen digest and RUNNING CAS
// prevent stale workers from changing terminal results or a later case revision.
func (m *Module) evaluateEvalRun(ctx context.Context, tenant, id guid.ID) error {
	var run model.EvalRun
	var view EvalRunView
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		if e := tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND id=?", tenant, id).Take(&run).Error; e != nil {
			return e
		}
		var e error
		view, e = evalRunView(&run)
		return e
	})
	if err != nil || run.State != "RUNNING" {
		return err
	}
	request := CalibrationEvaluationRequest{TenantID: tenant, RunID: id, TemplateVersionID: run.TemplateVersionID, DefinitionSHA256: view.Snapshot.DefinitionSHA256, Verifier: view.Snapshot.Verifier, ProviderRef: view.Snapshot.Target.ProviderRef, MachineID: view.Snapshot.Target.MachineID, Samples: []CalibrationInput{}}
	request.InitiatorID = run.CreatedBy
	request.Target = view.Snapshot.Target
	labeled := calibrationSnapshot{DefinitionSHA256: view.Snapshot.DefinitionSHA256, Verifier: view.Snapshot.Verifier, Samples: []CalibrationSampleView{}}
	sampleBytes := 0
	for _, c := range view.Snapshot.Cases {
		expected := c.Expected[view.Snapshot.Target.VerifierKey]
		if expected == "" {
			continue
		}
		input, _ := json.Marshal(struct {
			InputRef []EvalFileRef   `json:"input_ref"`
			Source   json.RawMessage `json:"source"`
		}{c.InputRef, c.SourceSnapshot})
		if view.Snapshot.Verifier.PluginKey == "http-v1" {
			var e error
			input, e = m.evalSampleInput(ctx, tenant, run.CreatedBy, c, true)
			if e != nil {
				continue
			}
			if sampleBytes+len(input) > 3<<20 {
				continue
			}
			sampleBytes += len(input)
			request.EvalCases = append(request.EvalCases, c)
		}
		// No Human expected verdict crosses the evaluator boundary.
		request.Samples = append(request.Samples, CalibrationInput{SampleID: c.ID, Input: input, SHA256: evalDigest(input)})
		labeled.Samples = append(labeled.Samples, CalibrationSampleView{ID: c.ID, Expected: expected})
	}
	state, limitations := "UNAVAILABLE", "Actual evaluator or target-specific Human labels unavailable; agreement unknown"
	results := []CalibrationResult{}
	var agreement *int
	passed, failed, inconclusive := 0, 0, run.CasesTotal
	if len(request.Samples) > 0 && m.calibrationEvaluator != nil {
		work, cancel := context.WithTimeout(ctx, 10*time.Minute)
		evaluation, e := m.calibrationEvaluator(work, request)
		cancel()
		if e == nil {
			score, valid := calibrationAgreement(labeled, evaluation, request.ProviderRef, request.MachineID)
			if valid == nil {
				state = "COMPLETED"
				limitations = "Frozen labelled cases only; unlabelled cases and unavailable verdicts remain inconclusive" //nolint:misspell // Preserve the published domain or serialized contract spelling.
				results = evaluation.Results
				inconclusive = run.CasesTotal - len(results)
				expected := map[guid.ID]string{}
				for _, s := range labeled.Samples {
					expected[s.ID] = s.Expected
				}
				for _, r := range results {
					if r.Verdict == "INCONCLUSIVE" {
						inconclusive++
					} else if r.Verdict == expected[r.SampleID] {
						passed++
					} else {
						failed++
					}
				}
				if inconclusive == 0 {
					agreement = &score
				}
			} else {
				state = "FAILED"
				limitations = "Evaluator returned incomplete or unsupported provenance; results rejected"
			}
		} else {
			limitations = "Actual evaluator failed; agreement unknown"
		}
	}
	encoded, _ := json.Marshal(results)
	now := time.Now().UTC()
	return m.transaction(ctx, func(tx *gorm.DB) error {
		q := query.BindVersions(tx, m.names)
		version, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(run.TemplateVersionID)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if version.SHA256 != view.Snapshot.DefinitionSHA256 {
			return ErrConflict
		}
		result := tx.WithContext(ctx).Table(m.names.EvalRuns.String()).Where("tenant_id=? AND id=? AND state=? AND sha256=?", tenant, id, "RUNNING", run.SHA256).Updates(map[string]any{"state": state, "results": string(encoded), "passed": passed, "failed": failed, "inconclusive": inconclusive, "agreement_ppm": agreement, "finished_at": now, "limitations": limitations})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return m.evalEventOn(ctx, tx, tenant, 0, id, "eval.run_completed", true, map[string]any{"state": state, "agreement_ppm": agreement, "passed": passed, "failed": failed, "inconclusive": inconclusive, "snapshot_sha256": run.SHA256})
	})
}
