package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"time"
)

func (m *Module) injectArchiveCalibration(s *module.Services) error {
	if err := m.injectVideo(s); err != nil {
		return err
	}
	c, err := module.Resolve[delivery.ProjectArchiveConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Bind("execution", m.projectArchiveResourcesOn)
}

// A platform process receipt is never tool-calibration proof. This projection
// consults the source-owned catalog on the task's actual machine and template.
// Uncalibrated deterministic tools cannot promote a receipt to a passing gate;
// model results remain advisory even after Human agreement calibration.
func (m *Module) verifierCalibrationOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, v *VerifierResult) error {
	if v.TemplatePin == nil || m.templateVerifiers.catalog.ToolCalibration == nil {
		return nil
	}
	state, e := m.templateVerifiers.catalog.ToolCalibration(ctx, tx, tenant, v.TemplatePin.TemplateVersionID, v.VerifierKey, machine)
	if e != nil {
		return e
	}
	if v.Kind == "MODEL_SCORED" {
		if m.templateVerifiers.catalog.GetOn != nil {
			_, calibrated, e := m.templateVerifiers.catalog.GetOn(ctx, tx, tenant, v.TemplatePin.TemplateVersionID, v.VerifierKey)
			if e != nil {
				return e
			}
			v.CalibrationState = calibrated
		}
		return nil
	}
	v.CalibrationState = state
	if state != "CALIBRATED" && v.Verdict == "PASSED" {
		v.Verdict = "INCONCLUSIVE"
		v.Reason = "TOOL_UNCALIBRATED"
	}
	return nil
}

func (m *Module) receiptCalibrationOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, p VerificationPlan) error {
	if p.TemplateVerifier == nil || m.templateVerifiers.catalog.ToolCalibration == nil || p.TemplateVerifier.Verifier.Kind != "DETERMINISTIC" {
		return nil
	}
	state, e := m.templateVerifiers.catalog.ToolCalibration(ctx, tx, tenant, p.TemplateVerifier.TemplateVersionID, p.TemplateVerifier.Verifier.Key, machine)
	if e != nil {
		return e
	}
	if state != "CALIBRATED" {
		return ErrConflict
	}
	return nil
}

// Runtime response permission is optional on the historical execution read.
// The archived project fence removes response capability without removing the
// already-authorized execution history.
func (m *Module) historicalResponseAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	err := m.humanAuthority(ctx, tx, tenant, project, "interaction_respond")
	if errors.Is(err, delivery.ErrWorkProjectReadOnly) {
		return ErrForbidden
	}
	return err
}
func (m *Module) projectArchiveResourcesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]delivery.ProjectArchiveResource, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	out := []delivery.ProjectArchiveResource{}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		// Terminal execution state is insufficient while a lease or cancellation
		// acknowledgement remains outstanding.
		ready := slices.Contains([]string{"SUCCEEDED", "FAILED", StateCanceled, "INTERRUPTED", "REJECTED"}, r.State) && r.LeaseExpiresAt == nil
		out = append(out, delivery.ProjectArchiveResource{Kind: "EXECUTION", ID: r.ID, State: r.State, Version: r.Version, Ready: ready})
		a := query.BindApprovals(tx, m.names)
		approvals, e := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ExecutionID.Eq(r.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(a.ID).Find()
		if e != nil {
			return nil, e
		}
		for _, v := range approvals {
			out = append(out, delivery.ProjectArchiveResource{Kind: "RUNTIME_APPROVAL", ID: v.ID, State: v.State, Version: 1, Ready: v.State != "PENDING" && v.DecidedAt != nil})
		}
	}
	p := query.BindPreviewSessions(tx, m.names)
	previews, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(p.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, v := range previews {
		out = append(out, delivery.ProjectArchiveResource{Kind: "PREVIEW_TUNNEL", ID: v.ID, State: v.State, Version: v.Version, Ready: v.State == "STOPPED" && v.EndedAt != nil})
	}
	vq := query.BindVerificationTasks(tx, m.names)
	verifications, err := vq.WithContext(ctx).Where(vq.TenantID.Eq(tenant), vq.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(vq.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, v := range verifications {
		out = append(out, delivery.ProjectArchiveResource{Kind: "VERIFICATION_TASK", ID: v.ID, State: v.State, Version: 1, Ready: v.State == "DONE" && json.Valid([]byte(v.ObservationJSON)) && v.ReceiptHash == hash([]byte(v.ObservationJSON))})
	}
	return out, nil
}

func (m *Module) injectVideo(s *module.Services) error {
	if err := m.injectRubricProof(s); err != nil {
		return err
	}
	c, err := module.Resolve[templates.VideoSourceConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = c.Bind(m.videoSourceOn); err != nil {
		return err
	}
	p, err := module.Resolve[templates.ProviderExecutionConnector](s)
	if err != nil {
		return err
	}
	return p.Bind(m.providerAuthorityOn)
}
func (m *Module) providerAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, operation string) error {
	if transaction(tx, tenant, project) != nil || id <= 0 {
		return ErrInvalidArgument
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return err
	}
	if row.ProjectID != project {
		return ErrNotFound
	}
	if !slices.Contains([]string{"GENERATE", "EXECUTE", "READ", "CANCEL"}, operation) {
		return ErrInvalidArgument
	}
	if operation == "READ" {
		return m.humanAuthority(ctx, tx, tenant, project, "execution_read")
	}
	actor := authn.PrincipalIDFromContext(ctx)
	// Background calls revalidate the recorded initiating Human, never a caller
	// supplied identity. System quality branches with no Human stay unavailable.
	if operation == "EXECUTE" {
		if row.InitiatorID <= 0 {
			return ErrCapabilityUnavailable
		}
		actor = row.InitiatorID
		ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, EntityID: project, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
	}
	if err = m.workPrincipalLocks(ctx, tx, link(row, actor), actor, row.InitiatorID); err != nil {
		return err
	}
	if err = m.humanAuthority(ctx, tx, tenant, project, "execution_start"); err != nil {
		return err
	}
	if m.work.AuthorizeOn == nil {
		return ErrCapabilityUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(row, actor), "RECEIPT"); err != nil {
		return err
	}
	row, err = m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return err
	}
	if row.State != "RUNNING" || row.CancelRequestedAt != nil || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) {
		return ErrConflict
	}
	return nil
}
func (m *Module) videoSourceOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (templates.VideoExecution, error) {
	out := templates.VideoExecution{Artifacts: []templates.VideoArtifact{}, QC: []templates.VideoQC{}}
	if transaction(tx, tenant, project) != nil || id <= 0 {
		return out, ErrInvalidArgument
	}
	if err := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); err != nil {
		return out, err
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return out, err
	}
	if row.ProjectID != project {
		return out, ErrNotFound
	}
	out.ExecutionID = row.ID
	out.WorkItemID = row.WorkItemID
	out.State = row.State
	q := query.BindEvidence(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ExecutionID.Eq(id), q.Kind.Eq("ARTIFACT")).Order(q.ID).Find()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		var a ArtifactFact
		if hash([]byte(r.Content)) != r.ContentHash || json.Unmarshal([]byte(r.Content), &a) != nil || a.BindingID <= 0 || a.FileID <= 0 || a.VersionID <= 0 || len(a.SHA256) != 64 {
			return out, ErrUnavailable
		}
		out.Artifacts = append(out.Artifacts, templates.VideoArtifact{EvidenceID: r.ID, BindingID: a.BindingID, FileID: a.FileID, VersionID: a.VersionID, SHA256: a.SHA256})
	}
	v := query.BindVerificationTasks(tx, m.names)
	tasks, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(project), v.ExecutionID.Eq(id), v.State.Eq("DONE")).Order(v.ID).Find()
	if err != nil {
		return out, err
	}
	for _, task := range tasks {
		var p VerificationPlan
		if hash([]byte(task.PlanJSON)) != task.PlanHash || json.Unmarshal([]byte(task.PlanJSON), &p) != nil {
			return out, ErrUnavailable
		}
		if p.TemplateVerifier == nil || p.TemplateVerifier.Verifier.Kind != "DETERMINISTIC" || !slices.Contains([]string{"ffprobe_spec", "av_qc", "loudness", "subtitle_rules", "subtitle_alignment", "shot_cut_match"}, p.TemplateVerifier.Verifier.Key) {
			continue
		}
		receipt, e := m.verificationReceiptOn(ctx, tx, tenant, project, row.WorkItemID, task.ResultID, task.ID)
		if e != nil {
			return out, e
		}
		out.QC = append(out.QC, templates.VideoQC{TaskID: task.ID, ResultID: task.ResultID, ResultVersion: task.ResultVersion, Verifier: p.TemplateVerifier.Verifier.Key, Method: receipt.Method, Stage: receipt.Stage, Verdict: receipt.Verdict, Reason: receipt.Reason, ReceiptSHA256: receipt.ReceiptHash, HeadCommit: receipt.HeadCommit})
	}
	return out, nil
}

func (m *Module) injectRubricProof(s *module.Services) error {
	c, e := module.Resolve[templates.RubricProofConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	return c.Bind(m.rubricProofOn)
}

func (m *Module) rubricProofOn(ctx context.Context, tx *gorm.DB, in templates.RubricProofRequest) (templates.RubricProofFact, error) {
	var out templates.RubricProofFact
	tenant := authn.TenantIDFromContext(ctx)
	if transaction(tx, tenant, in.ProjectID) != nil || in.ResultID <= 0 || len(in.VerifierKeys) != 1 {
		return out, ErrInvalidArgument
	}
	q := query.BindVerificationTasks(tx, m.names)
	tasks, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(in.ProjectID), q.WorkItemID.Eq(in.WorkItemID), q.ResultID.Eq(in.ResultID)).Order(q.ID.Desc()).Find()
	if e != nil {
		return out, e
	}
	for _, task := range tasks {
		var p VerificationPlan
		if hash([]byte(task.PlanJSON)) != task.PlanHash || json.Unmarshal([]byte(task.PlanJSON), &p) != nil {
			return out, ErrUnavailable
		}
		if p.TemplateVerifier == nil || p.TemplateVerifier.Verifier.Kind != "DETERMINISTIC" || p.TemplateVerifier.Verifier.Key != in.VerifierKeys[0] {
			continue
		}
		if in.TemplateVersionID > 0 && (p.TemplateVerifier.TemplateVersionID != in.TemplateVersionID || p.TemplateVerifier.DefinitionHash != in.DefinitionHash) {
			return out, ErrConflict
		}
		// The latest matching attempt owns the fact, including pending/failed states.
		// An earlier pass cannot hide a subsequent failed or unfinished check.
		r, e := m.verificationReceiptOn(ctx, tx, tenant, in.ProjectID, in.WorkItemID, in.ResultID, task.ID)
		if e != nil {
			return out, e
		}
		if r.ResultVersion != in.ResultVersion || r.HeadCommit != in.HeadCommit {
			return out, ErrConflict
		}
		return templates.RubricProofFact{TaskID: task.ID, ReceiptHash: r.ReceiptHash, VerifierKey: p.TemplateVerifier.Verifier.Key, Passed: r.Verdict == "PASSED"}, nil
	}
	return out, ErrCapabilityUnavailable
}
