package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VerificationExecutor is a trusted module capability. QueueOn is intentionally
// not an HTTP operation: delivery authorizes and pins the exact result version.
type VerificationExecutor struct {
	ReviewersOn       func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]guid.ID, error)
	SourcePrincipalOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)
	QueueOn           func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error)
	ReceiptOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error)
}
type VerificationRequest struct {
	AllowFamilyFallback                                                 bool
	ReviewerPrincipalID                                                 guid.ID
	Stage, CriterionText                                                string
	TenantID, ProjectID, WorkItemID, ResultID, ExecutionID, CriterionID guid.ID
	ResultVersion                                                       int
	Method, HeadCommit, ReportPath                                      string
	Argv                                                                []string
	TimeoutSec                                                          int
}
type VerificationReceipt struct {
	ReviewerID                                                 guid.ID
	Model, Family, Stage                                       string
	FamilyFallback                                             bool
	TaskID, ResultID, ExecutionID, CriterionID                 guid.ID
	ResultVersion                                              int
	Method, Verdict, Reason, ReceiptHash, PlanHash, HeadCommit string
	ExitCode                                                   *int
}

type VerificationPlan struct {
	RegistryVerifiers []RegistryVerifierPin `json:"registry_verifiers,omitempty"`
	Correlation       *Correlation          `json:"correlation,omitempty"`
	TemplateVerifier  *TemplateVerifierPin  `json:"template_verifier,omitempty"`
	ID                string                `json:"id"`
	Nonce             string                `json:"nonce"`
	ResultID          string                `json:"result_id"`
	ResultVersion     int                   `json:"result_version"`
	CriterionID       string                `json:"criterion_id"`
	Method            string                `json:"method"`
	SourceWorkdir     string                `json:"source_workdir"`
	HeadCommit        string                `json:"head_commit"`
	Argv              []string              `json:"argv"`
	TimeoutSec        int                   `json:"timeout_sec"`
	ReportPath        string                `json:"report_path"`
	ReviewerID        string                `json:"reviewer_id"`
	ReviewerRuntime   string                `json:"reviewer_runtime"`
	ReviewerModel     string                `json:"reviewer_model"`
	ReviewerFamily    string                `json:"reviewer_family"`
	Stage             string                `json:"stage"`
	CriterionText     string                `json:"criterion_text"`
	FamilyFallback    bool                  `json:"family_fallback"`
}
type VerificationObservation struct {
	Unverified      []string           `json:"unverified,omitempty" maxItems:"10"`
	Screenshots     []VisualScreenshot `json:"screenshots,omitempty" maxItems:"2"`
	BrowserProvider string             `json:"browser_provider,omitempty" maxLength:"200"`
	preparedVisual  []*files.PreparedExecutionArtifact
	PlanHash        string                   `json:"plan_hash"`
	HeadCommit      string                   `json:"head_commit"`
	ExitCode        *int                     `json:"exit_code,omitempty"`
	Reason          string                   `json:"reason"`
	OutputSHA256    string                   `json:"output_sha256"`
	ReportSHA256    string                   `json:"report_sha256"`
	StartedAt       time.Time                `json:"started_at"`
	EndedAt         time.Time                `json:"ended_at"`
	Passed          *int                     `json:"passed,omitempty"`
	Failed          *int                     `json:"failed,omitempty"`
	Skipped         *int                     `json:"skipped,omitempty"`
	AgentReport     *VerificationAgentReport `json:"agent_report,omitempty"`
}

type VerificationAgentFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}
type VerificationAgentReport struct {
	CriterionID string                     `json:"criterion_id"`
	HeadCommit  string                     `json:"head_commit"`
	Verdict     string                     `json:"verdict"`
	Findings    []VerificationAgentFinding `json:"findings"`
	Unverified  []string                   `json:"unverified"`
}

func (m *Module) provideVerificationExecutor(s *module.Services) error {
	if err := m.provideHiddenSource(s); err != nil {
		return err
	}
	return module.Provide[VerificationExecutor](s, VerificationExecutor{ReviewersOn: m.verificationReviewersOn, SourcePrincipalOn: m.verificationSourcePrincipalOn, QueueOn: m.queueVerificationOn, ReceiptOn: m.verificationReceiptOn})
}
func (m *Module) verificationSourcePrincipalOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return 0, err
	}
	v, err := view(r)
	if err != nil {
		return 0, err
	}
	if v.Plan.ExecutorPrincipalID <= 0 {
		return 0, ErrCapabilityUnavailable
	}
	return v.Plan.ExecutorPrincipalID, nil
}
func (m *Module) queueVerificationOn(ctx context.Context, tx *gorm.DB, in VerificationRequest) (guid.ID, error) {
	if transaction(tx, in.TenantID, in.ProjectID) != nil || in.WorkItemID <= 0 || in.ResultID <= 0 || in.ExecutionID <= 0 || in.CriterionID <= 0 || in.ResultVersion < 1 || !commitHash(in.HeadCommit) || in.TimeoutSec < 1 || in.TimeoutSec > 3600 {
		return 0, ErrInvalidArgument
	}
	if in.Method != "TEST" && in.Method != "COMMAND" && in.Method != "AGENT_JUDGE" && in.Method != "VISUAL" {
		return 0, ErrCapabilityUnavailable
	}
	if in.Method != "AGENT_JUDGE" && (len(in.Argv) < 1 || len(in.Argv) > 100 || in.Argv[0] == "" || strings.HasPrefix(in.Argv[0], "-")) {
		return 0, ErrInvalidArgument
	}
	if in.Method == "TEST" && (in.ReportPath == "" || filepath.IsAbs(in.ReportPath) || strings.ContainsAny(in.ReportPath, "\\:") || strings.HasPrefix(filepath.Clean(in.ReportPath), "..")) {
		return 0, ErrInvalidArgument
	}
	for _, arg := range in.Argv {
		if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
			return 0, ErrInvalidArgument
		}
	}
	if in.Method == "VISUAL" && !validVisualArguments(in.Argv) {
		return 0, ErrInvalidArgument
	}
	row, err := m.rowOn(ctx, tx, in.TenantID, in.ExecutionID, false)
	if err != nil {
		return 0, err
	}
	if row.ProjectID != in.ProjectID || row.WorkItemID != in.WorkItemID || row.State != "SUCCEEDED" {
		return 0, ErrConflict
	}
	v, err := view(row)
	if err != nil {
		return 0, err
	}
	if !filepath.IsAbs(v.Plan.Workdir) {
		return 0, ErrCapabilityUnavailable
	}
	e := query.BindEvidence(tx, m.names)
	count, err := e.WithContext(ctx).Where(e.TenantID.Eq(in.TenantID), e.ExecutionID.Eq(in.ExecutionID), e.Kind.Eq("COMMIT"), e.Content.Eq(in.HeadCommit)).Count()
	if err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, ErrConflict
	}
	id, err := m.nextID()
	if err != nil {
		return 0, err
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return 0, err
	}
	plan := VerificationPlan{ID: id.String(), Nonce: hex.EncodeToString(nonce), ResultID: in.ResultID.String(), ResultVersion: in.ResultVersion, CriterionID: in.CriterionID.String(), Method: in.Method, SourceWorkdir: v.Plan.Workdir, HeadCommit: in.HeadCommit, Argv: in.Argv, TimeoutSec: in.TimeoutSec, ReportPath: in.ReportPath}
	plan.Stage = in.Stage
	if in.Method == "AGENT_JUDGE" {
		if err = m.pinReviewerOn(ctx, tx, in, v.Plan, &plan); err != nil {
			return 0, err
		}
		plan.Argv = nil
		plan.ReportPath = ""
	}
	if err = m.pinTemplateVerifierOn(ctx, tx, in, &plan); err != nil {
		return 0, err
	}
	if err = m.pinRegistryVerifiersOn(ctx, tx, in, &plan); err != nil {
		return 0, err
	}
	plan.Correlation, err = newCorrelation(ctx, v.Plan.Correlation)
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return 0, err
	}
	task := &model.VerificationTask{ID: id, TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, ExecutionID: in.ExecutionID, ResultID: in.ResultID, CriterionID: in.CriterionID, ResultVersion: in.ResultVersion, MachineID: row.MachineID, State: "QUEUED", PlanJSON: string(encoded), PlanHash: hash(encoded), CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = query.BindVerificationTasks(tx, m.names).WithContext(ctx).Create(task); err != nil {
		return 0, err
	}
	return id, m.eventOn(withVerificationCorrelation(ctx, plan), tx, row, "execution.verification_queued", "SYSTEM", "verification", map[string]any{"task_id": id.String(), "result_id": in.ResultID.String(), "criterion_id": in.CriterionID.String()})
}

func verificationVerdict(p VerificationPlan, o VerificationObservation) (string, string) {
	if p.Method == "VISUAL" {
		return visualVerificationVerdict(p, o)
	}
	if p.Method == "AGENT_JUDGE" {
		return agentVerificationVerdict(p, o)
	}
	if o.Reason != "PROCESS_EXITED" || o.ExitCode == nil {
		return "INCONCLUSIVE", o.Reason
	}
	if o.HeadCommit != p.HeadCommit {
		return "INCONCLUSIVE", "HEAD_MISMATCH"
	}
	if *o.ExitCode < 0 {
		return "INCONCLUSIVE", "PROCESS_OUTCOME_UNKNOWN"
	}
	if decoded, err := hex.DecodeString(o.OutputSHA256); err != nil || len(decoded) != 32 {
		return "INCONCLUSIVE", "OUTPUT_DIGEST_INVALID"
	}
	if *o.ExitCode != 0 {
		return "FAILED", "NONZERO_EXIT"
	}
	if p.Method == "TEST" {
		reportDigest, reportErr := hex.DecodeString(o.ReportSHA256)
		if reportErr != nil || len(reportDigest) != 32 || o.Passed == nil || o.Failed == nil || o.Skipped == nil || *o.Passed < 0 || *o.Failed < 0 || *o.Skipped < 0 || (*o.Passed == 0 && *o.Failed == 0) {
			return "INCONCLUSIVE", "REPORT_UNPARSEABLE"
		}
		if *o.Failed > 0 {
			return "FAILED", "TEST_FAILURES"
		}
		if *o.Skipped > 0 {
			return "INCONCLUSIVE", "TESTS_SKIPPED"
		}
	}
	return "PASSED", "PROCESS_EXITED"
}
func (m *Module) verificationReceiptOn(ctx context.Context, tx *gorm.DB, tenant, project, work, result, taskID guid.ID) (VerificationReceipt, error) {
	var out VerificationReceipt
	if transaction(tx, tenant, taskID) != nil || project <= 0 || work <= 0 || result <= 0 {
		return out, ErrInvalidArgument
	}
	q := query.BindVerificationTasks(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work), q.ResultID.Eq(result), q.ID.Eq(taskID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if r.State != "DONE" {
		return out, ErrConflict
	}
	var p VerificationPlan
	var o VerificationObservation
	if hash([]byte(r.PlanJSON)) != r.PlanHash || hash([]byte(r.ObservationJSON)) != r.ReceiptHash || json.Unmarshal([]byte(r.PlanJSON), &p) != nil || json.Unmarshal([]byte(r.ObservationJSON), &o) != nil || o.PlanHash != r.PlanHash {
		return out, ErrUnavailable
	}
	if e := m.receiptCalibrationOn(ctx, tx, tenant, r.MachineID, p); e != nil {
		return out, e
	}
	verdict, reason := verificationVerdict(p, o)
	reviewerID, _ := guid.RequireID(p.ReviewerID)
	if p.Method == "VISUAL" && reason == "VISUAL_REVIEW_REQUIRED" {
		d, digest, err := m.visualReviewOn(ctx, tx, r)
		if err != nil {
			return out, err
		}
		if d == nil {
			return out, ErrConflict
		}
		if d.HeadCommit != p.HeadCommit {
			return out, ErrUnavailable
		}
		return VerificationReceipt{TaskID: r.ID, ResultID: r.ResultID, ExecutionID: r.ExecutionID, CriterionID: r.CriterionID, ResultVersion: r.ResultVersion, Method: p.Method, Verdict: d.Verdict, Reason: d.Reason, ReceiptHash: digest, PlanHash: r.PlanHash, HeadCommit: p.HeadCommit, ReviewerID: d.ReviewerID, Model: d.Model, Family: d.Family, Stage: p.Stage}, nil
	}
	return VerificationReceipt{TaskID: r.ID, ResultID: r.ResultID, ExecutionID: r.ExecutionID, CriterionID: r.CriterionID, ResultVersion: r.ResultVersion, Method: p.Method, Verdict: verdict, Reason: reason, ReceiptHash: r.ReceiptHash, PlanHash: r.PlanHash, HeadCommit: p.HeadCommit, ExitCode: o.ExitCode, ReviewerID: reviewerID, Model: p.ReviewerModel, Family: p.ReviewerFamily, Stage: p.Stage, FamilyFallback: p.FamilyFallback}, nil
}

func (m *Module) pullVerificationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) ([]VerificationPlan, error) {
	if transaction(tx, tenant, f.MachineID) != nil {
		return nil, ErrInvalidArgument
	}
	if err := m.validateRunner(ctx, tx, tenant, f); err != nil {
		return nil, err
	}
	q := query.BindVerificationTasks(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(f.MachineID), q.State.Eq("QUEUED")).Order(q.ID).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []VerificationPlan{}, nil
	}
	if err != nil {
		return nil, err
	}
	var p VerificationPlan
	if hash([]byte(r.PlanJSON)) != r.PlanHash || json.Unmarshal([]byte(r.PlanJSON), &p) != nil {
		return nil, ErrUnavailable
	}
	if m.machines.ReserveSlotOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	if _, err = m.machines.ReserveSlotOn(ctx, tx, tenant, f.MachineID, r.ID); err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(time.Duration(p.TimeoutSec+60) * time.Second)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.State.Eq("QUEUED")).Updates(map[string]any{"state": "LEASED", "session_id": f.SessionID, "conn_epoch": f.ConnEpoch, "lease_expires_at": expires})
	if err != nil {
		return nil, err
	}
	return []VerificationPlan{p}, nil
}
func (m *Module) completeVerificationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, f RunnerFence, nonce string, o VerificationObservation) error {
	if transaction(tx, tenant, id) != nil {
		return ErrInvalidArgument
	}
	if err := m.validateRunner(ctx, tx, tenant, f); err != nil {
		return err
	}
	q := query.BindVerificationTasks(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.MachineID.Eq(f.MachineID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var p VerificationPlan
	if json.Unmarshal([]byte(r.PlanJSON), &p) != nil || hash([]byte(r.PlanJSON)) != r.PlanHash {
		return ErrUnavailable
	}
	if p.Nonce != nonce || r.SessionID != f.SessionID || r.ConnEpoch != f.ConnEpoch || o.PlanHash != r.PlanHash {
		return ErrFenced
	}
	if p.Method != "VISUAL" && (len(o.Screenshots) > 0 || o.BrowserProvider != "") {
		return ErrInvalidArgument
	}
	encoded, err := json.Marshal(storedVerificationObservation(o))
	if err != nil {
		return err
	}
	if r.State == "DONE" {
		if hash(encoded) != r.ReceiptHash {
			return ErrConflict
		}
		return nil
	}
	if r.State != "LEASED" || r.LeaseExpiresAt == nil || !r.LeaseExpiresAt.After(time.Now()) || o.StartedAt.IsZero() || o.EndedAt.Before(o.StartedAt) || o.StartedAt.Before(r.CreatedAt) || o.EndedAt.After(time.Now().UTC().Add(time.Minute)) {
		return ErrFenced
	}
	if m.machines.ReleaseSlotOn == nil {
		return ErrCapabilityUnavailable
	}
	if p.Method == "VISUAL" {
		if err = m.attachVisualEvidenceOn(ctx, tx, r, p, o); err != nil {
			return err
		}
	}
	if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, f.MachineID, r.ID); err != nil {
		return err
	}
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id)).Updates(map[string]any{"state": "DONE", "observation_json": string(encoded), "receipt_hash": hash(encoded)})
	if err != nil {
		return err
	}
	row, err := m.rowOn(ctx, tx, tenant, r.ExecutionID, false)
	if err != nil {
		return err
	}
	verdict, reason := verificationVerdict(p, o)
	return m.eventOn(withVerificationCorrelation(ctx, p), tx, row, "execution.verification_completed", "MACHINE", "runner_verification", map[string]any{"task_id": id.String(), "verdict": verdict, "reason": reason, "receipt_hash": hash(encoded)})
}

// An interrupted verification is settled as unknown, never requeued or passed.
// The watchdog locks the machine before the task, matching runner receipt order.
func (m *Module) expireVerificationTasks(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindVerificationTasks(db, m.names)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("LEASED"), q.LeaseExpiresAt.Lte(now)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, candidate := range rows {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if m.machines.LockOn == nil || m.machines.ReleaseSlotOn == nil {
					return ErrCapabilityUnavailable
				}
				if e := m.lockMachineOn(ctx, tx, tenant, candidate.MachineID); e != nil {
					return e
				}
				tasks := query.BindVerificationTasks(tx, m.names)
				r, e := tasks.WithContext(ctx).Where(tasks.TenantID.Eq(tenant), tasks.ID.Eq(candidate.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
				if e != nil {
					return e
				}
				if r.State != "LEASED" || r.LeaseExpiresAt == nil || r.LeaseExpiresAt.After(now) {
					return nil
				}
				o := VerificationObservation{PlanHash: r.PlanHash, Reason: "MACHINE_UNAVAILABLE", StartedAt: now, EndedAt: now}
				encoded, e := json.Marshal(o)
				if e != nil {
					return e
				}
				if e = m.machines.ReleaseSlotOn(ctx, tx, tenant, r.MachineID, r.ID); e != nil {
					return e
				}
				if _, e = tasks.WithContext(ctx).Where(tasks.ID.Eq(r.ID)).Updates(map[string]any{"state": "DONE", "observation_json": string(encoded), "receipt_hash": hash(encoded)}); e != nil {
					return e
				}
				row, e := m.rowOn(ctx, tx, tenant, r.ExecutionID, false)
				if e != nil {
					return e
				}
				var plan VerificationPlan
				if hash([]byte(r.PlanJSON)) != r.PlanHash || json.Unmarshal([]byte(r.PlanJSON), &plan) != nil {
					return ErrUnavailable
				}
				return m.eventOn(withVerificationCorrelation(ctx, plan), tx, row, "execution.verification_completed", "SYSTEM", "verification_watchdog", map[string]any{"task_id": r.ID.String(), "verdict": "INCONCLUSIVE", "reason": "MACHINE_UNAVAILABLE", "receipt_hash": hash(encoded)})
			})
			if err != nil {
				return err
			}
		}
	}
}
