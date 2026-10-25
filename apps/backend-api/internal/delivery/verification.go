package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrVerificationPending = errors.New("delivery: required exact-result verification has not passed")

// VerificationExecutor is bound by the composition root to the execution owner.
// HTTP callers cannot submit executor verdicts; only a fenced persisted receipt
// obtained through this capability can conclude an automatic run.
type VerificationExecutor struct {
	QualityPrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	LaunchQualityOn     func(context.Context, *gorm.DB, QualityLaunchRequest) (guid.ID, error)
	ReviewersOn         func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]guid.ID, error)
	SourcePrincipalOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)
	QueueOn             func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error)
	ReceiptOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (VerificationReceipt, error)
}
type VerificationAutomation struct {
	QueueOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	CollectOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
}

type VerificationExecutorConnector struct {
	Bind func(VerificationExecutor) error
}
type VerificationRequest struct {
	AllowFamilyFallback                                                 bool
	ReviewerPrincipalID                                                 guid.ID
	Stage, CriterionText                                                string
	TenantID, ProjectID, WorkItemID, ResultID, ExecutionID, CriterionID guid.ID
	ResultVersion                                                       int
	Method, HeadCommit                                                  string
	Argv                                                                []string
	TimeoutSec                                                          int
	ReportPath                                                          string
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

func (m *Module) provideVerification(s *module.Services) error {
	if err := module.Provide[VerificationAutomation](s, VerificationAutomation{QueueOn: m.queueAutomaticVerificationOn, CollectOn: m.collectAutomaticVerificationOn}); err != nil {
		return err
	}
	return module.Provide[VerificationExecutorConnector](s, VerificationExecutorConnector{Bind: func(executor VerificationExecutor) error {
		if executor.QueueOn == nil || executor.ReceiptOn == nil || m.verificationExecutor.QueueOn != nil {
			return ErrInvalidArgument
		}
		m.verificationExecutor = executor
		return nil
	}})
}

type verificationStartBody struct {
	Stage               string   `json:"stage,omitempty" enum:"AUTOMATED,AGENT_TEST,AGENT_ACCEPTANCE"`
	ReviewerPrincipalID guid.ID  `json:"reviewer_principal_id,omitempty"`
	CriterionID         guid.ID  `json:"criterion_id"`
	Method              string   `json:"method" enum:"TEST,COMMAND,VISUAL,AGENT_JUDGE"`
	Argv                []string `json:"argv" maxItems:"64"`
	TimeoutSec          int      `json:"timeout_sec" minimum:"1" maximum:"3600"`
	ReportPath          string   `json:"report_path,omitempty" maxLength:"512"`
}
type verificationView struct {
	ReviewerID     *guid.ID `json:"reviewer_id,omitempty"`
	ReviewerModel  string   `json:"reviewer_model"`
	ReviewerFamily string   `json:"reviewer_family"`
	FamilyFallback bool     `json:"family_fallback"`
	ID             guid.ID  `json:"id"`
	CriterionID    guid.ID  `json:"criterion_id"`
	Method         string   `json:"method"`
	Stage          string   `json:"stage"`
	Round          int      `json:"round"`
	State          string   `json:"state"`
	Verdict        string   `json:"verdict"`
	Reason         string   `json:"reason"`
	ActorID        guid.ID  `json:"actor_id"`
	TaskID         *guid.ID `json:"task_id,omitempty"`
	CheckID        *guid.ID `json:"check_id,omitempty"`
	ReceiptHash    string   `json:"receipt_hash"`
}
type completionLadder struct {
	L1                 bool `json:"l1_claimed"`
	L2                 bool `json:"l2_internal_qa_passed"`
	L3                 bool `json:"l3_team_accepted"`
	L4                 bool `json:"l4_client_accepted"`
	InternalQARequired bool `json:"internal_qa_required"`
	HumanOverride      bool `json:"human_override"`
}

func verificationResultDigest(row *model.ResultVersion, evidence []*model.ResultEvidence) string {
	raw, _ := json.Marshal(struct {
		ID                 guid.ID
		Sequence, Baseline int
		Head               string
		Evidence           []*model.ResultEvidence
	}{row.ID, row.Sequence, row.BaselineVersionNo, row.HeadCommit, evidence})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Module) verificationRunsOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion) ([]*model.VerificationRun, error) {
	q := query.BindVerificationRuns(tx, m.service.names)
	return q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.WorkItemID.Eq(row.WorkItemID), q.ResultID.Eq(row.ID)).Order(q.ID.Desc()).Find()
}

// A Human CHECK is itself the immutable source record for manual verification;
// it does not become an automatic or Agent verdict.
func (m *Module) recordHumanVerificationOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, check *model.ResultAction) error {
	q := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Order(q.Position).Find()
	if err != nil {
		return err
	}
	now := check.CreatedAt
	var plan []workVerificationStep
	if row.VerificationPlan != "" {
		if err = json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
			return err
		}
	}
	methods := map[guid.ID]string{}
	for _, step := range plan {
		methods[step.CriterionID] = step.Method
	}
	for _, criterion := range criteria {
		// Execution claims need independent executor-backed methods; a manual check
		// cannot silently replace a declared deterministic/Agent verification plan.
		if methods[criterion.ID] != "HUMAN" {
			continue
		}
		id, e := m.ids.Next()
		if e != nil {
			return e
		}
		run := &model.VerificationRun{TenantID: row.TenantID, ID: guid.ID(id), ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ResultID: row.ID, CriterionID: criterion.ID, Method: "HUMAN", Stage: "HUMAN", Round: 1, State: "DONE", Verdict: check.Verdict, Reason: check.Reason, ActorID: check.ActorID, CheckID: &check.ID, CreatedAt: now, CompletedAt: &now}
		if e = query.BindVerificationRuns(tx, m.service.names).WithContext(ctx).Create(run); e != nil {
			return e
		}
	}
	return nil
}

func (m *Module) verificationParticipantScopeOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result, actor, owner guid.ID, in verificationStartBody) ([]string, bool, error) {
	var reviewerOwners []string
	reviewerScoped := false
	if in.Stage == "" {
		in.Stage = "AUTOMATED"
	}
	if in.Method == "AGENT_JUDGE" {
		if !slices.Contains([]string{"AGENT_TEST", "AGENT_ACCEPTANCE"}, in.Stage) || in.ReviewerPrincipalID <= 0 {
			return nil, false, ErrInvalidArgument
		}
		if m.verificationExecutor.SourcePrincipalOn == nil {
			return nil, false, ErrUnavailable
		}
		source, e := m.resultOn(ctx, tx, tenant, project, workID, result)
		if e != nil {
			return nil, false, e
		}
		if source.SourceExecutionID == nil {
			return nil, false, ErrResultState
		}
		principal, e := m.verificationExecutor.SourcePrincipalOn(ctx, tx, tenant, *source.SourceExecutionID)
		if e != nil {
			return nil, false, e
		}
		if principal <= 0 || principal == in.ReviewerPrincipalID {
			return nil, false, ErrResultIndependent
		}
		if e = m.lockStagePrincipalsOn(ctx, tx, actor, []guid.ID{owner, principal, in.ReviewerPrincipalID}); e != nil {
			return nil, false, e
		}
		active, e := m.members.IsMemberActiveOn(ctx, tx, tenant, in.ReviewerPrincipalID)
		if e != nil {
			return nil, false, e
		}
		if !active {
			return nil, false, ErrInvalidMember
		}
		constraint, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", in.ReviewerPrincipalID)
		if e != nil {
			return nil, false, e
		}
		if !constraint.AllowsEntityRows(project) {
			return nil, false, ErrWorkForbidden
		}
		reviewerScoped = !constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, project.String())
		reviewerOwners = constraint.OwnerIDs
	} else if in.Stage != "AUTOMATED" || in.ReviewerPrincipalID != 0 {
		return nil, false, ErrInvalidArgument
	}
	return reviewerOwners, reviewerScoped, nil
}

func (m *Module) startVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result, actor, owner guid.ID, expected int, in verificationStartBody) (ResultView, error) {
	var empty ResultView
	reviewerOwners, reviewerScoped, err := m.verificationParticipantScopeOn(ctx, tx, tenant, project, workID, result, actor, owner, in)
	if err != nil {
		return empty, err
	}
	if in.Stage == "" {
		in.Stage = "AUTOMATED"
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, workID, result, actor, owner, expected, "verification_run")
	if err != nil {
		return empty, err
	}
	if reviewerScoped && (work.CreatedBy == nil || !slices.Contains(reviewerOwners, work.CreatedBy.String())) {
		return empty, ErrWorkForbidden
	}
	maxRounds, err := verificationRoundLimit(work)
	if err != nil {
		return empty, err
	}
	if work.InternalRoundCount >= maxRounds {
		return empty, ErrVerificationPending
	}
	if !slices.Contains([]string{"VERIFYING", "NEEDS_HUMAN_REVIEW", "INTERNAL_QA"}, work.State) || work.BaselineStale || row.SourceExecutionID == nil || row.HeadCommit == "" {
		return empty, ErrResultState
	}
	if !slices.Contains([]string{"TEST", "COMMAND", "AGENT_JUDGE", "VISUAL"}, in.Method) || in.TimeoutSec < 1 || in.TimeoutSec > 3600 || len(in.Argv) > 64 || (in.Method != "AGENT_JUDGE" && len(in.Argv) == 0) {
		return empty, ErrInvalidArgument
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criterion, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.WorkItemID.Eq(workID), c.ID.Eq(in.CriterionID)).First()
	if e != nil {
		return empty, e
	}
	// A rerun cannot downgrade the frozen criterion's declared method.
	var plan []workVerificationStep
	if e = json.Unmarshal([]byte(row.VerificationPlan), &plan); e != nil {
		return empty, e
	}
	var frozen *workVerificationStep
	for i := range plan {
		if plan[i].CriterionID == in.CriterionID {
			frozen = &plan[i]
			break
		}
	}
	if frozen == nil {
		return empty, ErrInvalidArgument
	}
	if in.Method == "AGENT_JUDGE" {
		if len(in.Argv) != 0 || in.ReportPath != "" || frozen.Method == "HUMAN" || frozen.Method == "VISUAL" {
			return empty, ErrInvalidArgument
		}
		if e = m.verificationStageReadyOn(ctx, tx, work, row, in.Stage); e != nil {
			return empty, e
		}
	} else if frozen.Method != in.Method || !slices.Equal(frozen.Argv, in.Argv) || frozen.TimeoutSec != in.TimeoutSec || frozen.ReportPath != in.ReportPath {
		return empty, ErrInvalidArgument
	}
	if m.verificationExecutor.QueueOn == nil {
		return empty, ErrUnavailable
	}
	task, e := m.verificationExecutor.QueueOn(ctx, tx, VerificationRequest{ReviewerPrincipalID: in.ReviewerPrincipalID, Stage: in.Stage, CriterionText: criterion.Text, TenantID: tenant, ProjectID: project, WorkItemID: workID, ResultID: result, ExecutionID: *row.SourceExecutionID, CriterionID: in.CriterionID, ResultVersion: row.Sequence, Method: in.Method, HeadCommit: row.HeadCommit, Argv: in.Argv, TimeoutSec: in.TimeoutSec, ReportPath: in.ReportPath})
	if e != nil {
		return empty, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return empty, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := &model.VerificationRun{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: workID, ResultID: result, CriterionID: in.CriterionID, Method: in.Method, Stage: in.Stage, Round: work.InternalRoundCount + 1, State: "PENDING", ActorID: actor, TaskID: &task, CreatedAt: now}
	if in.Method == "AGENT_JUDGE" {
		run.ReviewerID = &in.ReviewerPrincipalID
	}
	if e = query.BindVerificationRuns(tx, m.service.names).WithContext(ctx).Create(run); e != nil {
		return empty, e
	}
	if e = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "VERIFICATION_QUEUED", "permission:verification_run", result, now); e != nil {
		return empty, e
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) collectVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result, actor, owner guid.ID, expected int) (ResultView, error) {
	var empty ResultView
	if err := m.prelockQualityOn(ctx, tx, tenant, project, workID, result, actor, owner); err != nil {
		return empty, err
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, workID, result, actor, owner, expected, "verification_run")
	if err != nil {
		return empty, err
	}
	return m.collectLockedVerificationOn(ctx, tx, work, row, actor, false)
}

func (m *Module) collectLockedVerificationOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, actor guid.ID, automatic bool) (ResultView, error) {
	var empty ResultView
	tenant, project, workID, result := row.TenantID, row.ProjectID, row.WorkItemID, row.ID
	if m.verificationExecutor.ReceiptOn == nil || row.SourceExecutionID == nil {
		return empty, ErrUnavailable
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	actorKind, authority := "HUMAN", "permission:verification_run"
	if automatic {
		actorKind, authority = "SYSTEM", "SYSTEM:verification"
	}
	count := 0
	for _, run := range runs {
		if run.State != "PENDING" || run.TaskID == nil {
			continue
		}
		receipt, e := m.verificationExecutor.ReceiptOn(ctx, tx, tenant, project, workID, result, *run.TaskID)
		if e != nil {
			if automatic && errors.Is(e, ErrResultState) {
				continue
			}
			return empty, e
		}
		if receipt.TaskID != *run.TaskID || receipt.ResultID != row.ID || receipt.ResultVersion != row.Sequence || receipt.CriterionID != run.CriterionID || receipt.ExecutionID != *row.SourceExecutionID || receipt.Method != run.Method || receipt.HeadCommit != row.HeadCommit || len(receipt.ReceiptHash) != 64 || !slices.Contains([]string{"PASSED", "FAILED", "INCONCLUSIVE"}, receipt.Verdict) {
			return empty, ErrInvalidArgument
		}
		if run.Method == "AGENT_JUDGE" && (run.ReviewerID == nil || receipt.ReviewerID != *run.ReviewerID || receipt.Stage != run.Stage || receipt.Model == "" || receipt.Family == "") {
			return empty, ErrInvalidArgument
		}
		if receipt.Method != "AGENT_JUDGE" && receipt.Method != "VISUAL" && receipt.Verdict == "PASSED" && (receipt.ExitCode == nil || *receipt.ExitCode != 0) {
			return empty, ErrInvalidArgument
		}
		q := query.BindVerificationRuns(tx, m.service.names)
		changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(run.ID), q.State.Eq("PENDING")).UpdateSimple(q.State.Value("DONE"), q.Verdict.Value(receipt.Verdict), q.Reason.Value(receipt.Reason), q.ReceiptHash.Value(receipt.ReceiptHash), q.ReviewerModel.Value(receipt.Model), q.ReviewerFamily.Value(receipt.Family), q.FamilyFallback.Value(receipt.FamilyFallback), q.CompletedAt.Value(now))
		if e != nil {
			return empty, e
		}
		if changed.RowsAffected != 1 {
			return empty, ErrWorkVersion
		}
		if _, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "verification.completed", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "work_item", ID: workID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"verification_id": run.ID.String(), "claim_id": result.String(), "work_item_id": workID.String(), "criterion_id": run.CriterionID.String(), "method": run.Method, "stage": run.Stage, "round": run.Round, "result": receipt.Verdict, "receipt_hash": receipt.ReceiptHash}}); e != nil {
			return empty, e
		}
		count++
	}
	if count == 0 && !automatic {
		return empty, ErrVerificationPending
	}
	current, e := m.verificationRunsOn(ctx, tx, row)
	if e != nil {
		return empty, e
	}
	failed, pending := false, false
	for _, run := range current {
		if run.Round != work.InternalRoundCount+1 || run.Method == "HUMAN" {
			continue
		}
		pending = pending || run.State == "PENDING"
		failed = failed || run.Verdict == "FAILED"
	}
	hiddenGate := m.hiddenAcceptanceGateOn(ctx, tx, work, row)
	hiddenFailed := errors.Is(hiddenGate, ErrHiddenAcceptanceFailed)
	before := work.State
	stageChanged := false
	if isRuntimeDefect(work, row) && work.State == "VERIFYING" {
		if fact, mergeErr := m.trustedDefectMergeOn(ctx, tx, work, row); mergeErr == nil && fact.SourceCommit == row.HeadCommit && work.DefectStage == nil {
			stage := "MERGED"
			work.DefectStage = &stage
			stageChanged = true
		}
		if m.defectDeploymentReadyOn(ctx, tx, work, row) == nil && m.verificationStageReadyOn(ctx, tx, work, row, "AGENT_TEST") == nil {
			work.State = "INTERNAL_QA"
		}
	}
	if hiddenFailed && !pending && work.State == "INTERNAL_QA" {
		work.InternalRoundCount++
		reason := "MANDATORY_HUMAN_GATE"
		work.EscalationReason = &reason
		work.RequireHumanDecision = true
		work.State = "NEEDS_HUMAN_REVIEW"
		if _, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.escalated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:verification"}, Subject: events.Subject{Kind: "work_item", ID: workID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"work_item_id": workID.String(), "reason_code": "HIDDEN_ACCEPTANCE_FAILED", "internal_round_count": work.InternalRoundCount}}); e != nil {
			return empty, e
		}
	} else if failed && !pending {
		work.InternalRoundCount++
		work.State = "REWORK"
		work.DefectStage = nil
		{
			maxRounds, e := verificationRoundLimit(work)
			if e != nil {
				return empty, e
			}
			if work.InternalRoundCount >= maxRounds {
				reason := "INTERNAL_ROUNDS_EXHAUSTED"
				work.EscalationReason = &reason
				work.RequireHumanDecision = true
				work.State = "NEEDS_HUMAN_REVIEW"
				if _, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.escalated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:verification"}, Subject: events.Subject{Kind: "work_item", ID: workID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"work_item_id": workID.String(), "reason_code": reason, "internal_round_count": work.InternalRoundCount}}); e != nil {
					return empty, e
				}
			} else {
				work.State = "REWORK"
			}
		}
		if work.State == "REWORK" {
			if e = m.relaunchQualityOn(ctx, tx, work, row, current, now); e != nil {
				return empty, e
			}
		}
	} else if !pending && work.State == "INTERNAL_QA" && m.verificationGateOn(ctx, tx, work, row, true) == nil {
		work.State = "NEEDS_HUMAN_REVIEW"
		if isRuntimeDefect(work, row) {
			stage := "VERIFIED"
			work.DefectStage = &stage
		}
		if e = m.recordReportQualityOn(ctx, tx, tenant, project, workID, "INTERNAL_QA_PASSED", 0, work.InternalRoundCount+1, now, row.ID); e != nil {
			return empty, e
		}
	}

	if count == 0 && work.State == before && !stageChanged {
		return empty, nil
	}
	if (work.InternalQARequired || failed) && row.SourceExecutionID != nil {
		actor = 0
		authority = "SYSTEM:verification"
		ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: tenant})
	}
	changeReason := "VERIFICATION_COMPLETED"
	if hiddenFailed {
		changeReason = "HIDDEN_ACCEPTANCE_FAILED"
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, changeReason, authority, row.ID, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

// Each stage consumes complete exact-result facts from its predecessor. A pass
// for one criterion cannot open review for unrelated unverified criteria.
func (m *Module) verificationStageReadyOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, stage string) error {
	if stage == "AGENT_ACCEPTANCE" {
		if err := m.claimAcceptanceEvidenceOn(ctx, tx, row); err != nil {
			return err
		}
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return err
	}
	allowed, err := m.verificationAllowedOverridesOn(ctx, tx, work, row)
	if err != nil {
		return err
	}
	var plan []workVerificationStep
	if err = json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
		return err
	}
	latest := map[string]*model.VerificationRun{}
	for _, run := range runs {
		key := run.CriterionID.String() + ":" + run.Stage
		if latest[key] == nil {
			latest[key] = run
		}
	}
	for _, step := range plan {
		if step.Method == "HUMAN" {
			continue
		}
		required := "AUTOMATED"
		if stage == "AGENT_ACCEPTANCE" {
			required = "AGENT_TEST"
		}
		if required == "AUTOMATED" && step.Method == "AGENT_JUDGE" {
			continue
		}
		run := latest[step.CriterionID.String()+":"+required]
		if run == nil || run.State != "DONE" {
			return ErrVerificationPending
		}
		if run.Verdict != "PASSED" {
			original, ok := allowed[run.ID]
			if !ok || original.Verdict != run.Verdict || original.ReceiptHash != run.ReceiptHash {
				return ErrVerificationPending
			}
		}
	}
	return nil
}

// verificationRoundLimit reads the canonical work budget for every dispatch path.
func verificationRoundLimit(work *model.WorkItem) (int, error) {
	limit := 5
	if work.Budget != nil {
		var budget workBudget
		if err := json.Unmarshal([]byte(*work.Budget), &budget); err != nil {
			return 0, err
		}
		if budget.MaxInternalRounds > 0 {
			limit = budget.MaxInternalRounds
		}
	}
	return limit, nil
}

// Automation is available only to internal event adapters. Durable outbox
// dispatch retries it after the source execution transaction has committed.
func (m *Module) lockAutomaticVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result guid.ID, additionalPrincipals ...guid.ID) (*model.WorkItem, *model.ResultVersion, map[string][]guid.ID, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || workID <= 0 || result <= 0 {
		return nil, nil, nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, nil, ErrInvalidArgument
	}
	row, err := m.resultOn(ctx, tx, tenant, project, workID, result)
	if errors.Is(err, ErrProjectNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if row.SourceExecutionID == nil || *row.SourceExecutionID != result {
		return nil, nil, nil, nil
	}
	w := query.BindWorkItems(tx, m.service.names)
	observed, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID)).First()
	if err != nil {
		return nil, nil, nil, err
	}
	reviewers := map[string][]guid.ID{}
	participants := append([]guid.ID{observed.AccountablePrincipalID}, additionalPrincipals...)
	hiddenPrincipals, err := m.hiddenAcceptancePrincipalsOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return nil, nil, nil, err
	}
	participants = append(participants, hiddenPrincipals...)
	if m.verificationExecutor.QualityPrincipalsOn != nil {
		ids, e := m.verificationExecutor.QualityPrincipalsOn(ctx, tx, tenant, *row.SourceExecutionID)
		if e != nil {
			return nil, nil, nil, e
		}
		participants = append(participants, ids...)
	}
	if m.verificationExecutor.ReviewersOn != nil {
		for _, stage := range []string{"AGENT_TEST", "AGENT_ACCEPTANCE"} {
			ids, e := m.verificationExecutor.ReviewersOn(ctx, tx, tenant, *row.SourceExecutionID, stage)
			if e != nil {
				return nil, nil, nil, e
			}
			reviewers[stage] = ids
			participants = append(participants, ids...)
		}
	}
	if m.verificationExecutor.SourcePrincipalOn != nil {
		source, e := m.verificationExecutor.SourcePrincipalOn(ctx, tx, tenant, *row.SourceExecutionID)
		if e == nil && source > 0 {
			participants = append(participants, source)
		} else if e != nil && !errors.Is(e, ErrUnavailable) {
			return nil, nil, nil, e
		}
	}
	lockActor := row.ActorID
	if lockActor <= 0 {
		lockActor = observed.AccountablePrincipalID
	}
	if err = automationPrincipalLockFence(ctx, participants, lockActor); err != nil {
		return nil, nil, nil, err
	}
	if err = m.lockStagePrincipalsOn(ctx, tx, lockActor, participants); err != nil {
		return nil, nil, nil, err
	}
	if m.verificationExecutor.QualityPrincipalsOn != nil {
		if err = m.qualityIAMFenceOn(ctx, tx, tenant, project, workID); err != nil {
			return nil, nil, nil, err
		}
	}
	for stage, ids := range reviewers {
		allowed := []guid.ID{}
		for _, principal := range ids {
			active, e := m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
			if e != nil {
				return nil, nil, nil, e
			}
			if !active {
				continue
			}
			constraint, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", principal)
			if e != nil {
				return nil, nil, nil, e
			}
			if !constraint.AllowsEntityRows(project) {
				continue
			}
			if !constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, project.String()) && (observed.CreatedBy == nil || !slices.Contains(constraint.OwnerIDs, observed.CreatedBy.String())) {
				continue
			}
			allowed = append(allowed, principal)
		}
		reviewers[stage] = allowed
	}
	p := query.BindProjects(tx, m.service.names)
	current, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	work, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID)).First()
	if err != nil {
		return nil, nil, nil, err
	}
	if work.AccountablePrincipalID != observed.AccountablePrincipalID {
		return nil, nil, nil, ErrWorkVersion
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, current.Stage) || work.ArchivedAt != nil || work.PausedAt != nil || work.BaselineStale || work.BaselineVersionNo != row.BaselineVersionNo || work.CurrentClaimID == nil || *work.CurrentClaimID != result || !slices.Contains([]string{"VERIFYING", "NEEDS_HUMAN_REVIEW", "INTERNAL_QA"}, work.State) { //nolint:misspell // Frozen business state.
		return nil, nil, nil, nil
	}
	return work, row, reviewers, nil
}
func (m *Module) queueAutomaticVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result guid.ID) error {
	work, row, reviewers, err := m.lockAutomaticVerificationOn(ctx, tx, tenant, project, workID, result)
	if err != nil || row == nil {
		return err
	}
	if err := m.dispatchHiddenAcceptanceOn(ctx, tx, work, row); err != nil {
		return err
	}
	maxRounds, err := verificationRoundLimit(work)
	if err != nil {
		return err
	}
	if work.InternalRoundCount >= maxRounds {
		return nil
	}
	if m.verificationExecutor.QueueOn == nil {
		return ErrUnavailable
	}
	var plan []workVerificationStep
	if err = json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
		return err
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return err
	}
	queued := false
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, step := range plan {
		if step.Method != "TEST" && step.Method != "COMMAND" && step.Method != "VISUAL" {
			continue
		}
		// Older VISUAL plans contain only descriptive detail. They remain pending
		// until a runnable target is frozen; do not break result submission.
		if step.Method == "VISUAL" && (len(step.Argv) == 0 || step.TimeoutSec < 1) {
			continue
		}
		exists := false
		for _, run := range runs {
			if run.CriterionID == step.CriterionID && run.Stage == "AUTOMATED" {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		task, e := m.verificationExecutor.QueueOn(ctx, tx, VerificationRequest{TenantID: tenant, ProjectID: project, WorkItemID: workID, ResultID: result, ExecutionID: *row.SourceExecutionID, CriterionID: step.CriterionID, ResultVersion: row.Sequence, Method: step.Method, Stage: "AUTOMATED", HeadCommit: row.HeadCommit, Argv: step.Argv, TimeoutSec: step.TimeoutSec, ReportPath: step.ReportPath})
		if e != nil {
			return e
		}
		id, e := m.ids.Next()
		if e != nil {
			return e
		}
		run := &model.VerificationRun{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: workID, ResultID: result, CriterionID: step.CriterionID, Method: step.Method, Stage: "AUTOMATED", Round: work.InternalRoundCount + 1, State: "PENDING", ActorID: 0, TaskID: &task, CreatedAt: now}
		if e = query.BindVerificationRuns(tx, m.service.names).WithContext(ctx).Create(run); e != nil {
			return e
		}
		queued = true
	}
	if queued {
		if err = m.resultWorkChangedOn(ctx, tx, work, 0, work.State, "VERIFICATION_QUEUED", "SYSTEM:verification", result, now); err != nil {
			return err
		}
	}
	return m.queueAutomaticAgentStagesOn(ctx, tx, work, row, reviewers)
}
func (m *Module) collectAutomaticVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result guid.ID) error {
	work, row, reviewers, err := m.lockAutomaticVerificationOn(ctx, tx, tenant, project, workID, result)
	if err != nil || row == nil {
		return err
	}
	_, err = m.collectLockedVerificationOn(ctx, tx, work, row, 0, true)
	if err != nil {
		return err
	}
	if work.State != "INTERNAL_QA" && work.State != "NEEDS_HUMAN_REVIEW" {
		return nil
	}
	return m.queueAutomaticAgentStagesOn(ctx, tx, work, row, reviewers)
}

func (m *Module) queueAutomaticAgentStagesOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, reviewers map[string][]guid.ID) error {
	if isRuntimeDefect(work, row) && work.State == "VERIFYING" {
		return nil
	}
	if !work.InternalQARequired || m.verificationExecutor.QueueOn == nil {
		return nil
	}
	maxRounds, err := verificationRoundLimit(work)
	if err != nil {
		return err
	}
	if work.InternalRoundCount >= maxRounds {
		return nil
	}
	var plan []workVerificationStep
	if err := json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
		return err
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return err
	}
	criteria := query.BindWorkCriteria(tx, m.service.names)
	rows, err := criteria.WithContext(ctx).Where(criteria.TenantID.Eq(work.TenantID), criteria.ProjectID.Eq(work.ProjectID), criteria.WorkItemID.Eq(work.ID)).Find()
	if err != nil {
		return err
	}
	texts := map[guid.ID]string{}
	for _, criterion := range rows {
		texts[criterion.ID] = criterion.Text
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	queued := false
	for _, stage := range []string{"AGENT_TEST", "AGENT_ACCEPTANCE"} {
		if err = m.verificationStageReadyOn(ctx, tx, work, row, stage); errors.Is(err, ErrVerificationPending) {
			continue
		} else if err != nil {
			return err
		}
		pendingSteps := []workVerificationStep{}
		for _, step := range plan {
			if step.Method == "HUMAN" || step.Method == "VISUAL" {
				continue
			}
			exists := false
			for _, run := range runs {
				if run.CriterionID == step.CriterionID && run.Stage == stage {
					exists = true
					break
				}
			}
			if !exists {
				pendingSteps = append(pendingSteps, step)
			}
		}
		if len(pendingSteps) == 0 {
			continue
		}
		if len(reviewers[stage]) == 0 {
			if m.verificationExecutor.ReviewersOn == nil {
				continue
			}
			if work.RequireHumanDecision && work.EscalationReason != nil && *work.EscalationReason == "MANDATORY_HUMAN_GATE" {
				return nil
			}
			reason := "MANDATORY_HUMAN_GATE"
			before := work.State
			work.State = "NEEDS_HUMAN_REVIEW"
			work.RequireHumanDecision = true
			work.EscalationReason = &reason
			if _, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "work_item.escalated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:verification"}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"work_item_id": work.ID.String(), "reason_code": reason, "detail": map[string]any{"reason": "NO_INTERNAL_QA_AGENT", "stage": stage}}}); err != nil {
				return err
			}
			return m.resultWorkChangedOn(ctx, tx, work, 0, before, "VERIFICATION_REVIEWER_UNAVAILABLE", "SYSTEM:verification", row.ID, now)
		}
		for _, step := range pendingSteps {
			reviewer := reviewers[stage][0]
			task, e := m.verificationExecutor.QueueOn(ctx, tx, VerificationRequest{AllowFamilyFallback: true, ReviewerPrincipalID: reviewer, Stage: stage, CriterionText: texts[step.CriterionID], TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ResultID: row.ID, ExecutionID: *row.SourceExecutionID, CriterionID: step.CriterionID, ResultVersion: row.Sequence, Method: "AGENT_JUDGE", HeadCommit: row.HeadCommit, TimeoutSec: 300})
			if e != nil {
				return e
			}
			id, e := m.ids.Next()
			if e != nil {
				return e
			}
			run := &model.VerificationRun{TenantID: row.TenantID, ID: guid.ID(id), ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ResultID: row.ID, CriterionID: step.CriterionID, Method: "AGENT_JUDGE", Stage: stage, Round: work.InternalRoundCount + 1, State: "PENDING", ActorID: 0, ReviewerID: &reviewer, TaskID: &task, CreatedAt: now}
			if e = query.BindVerificationRuns(tx, m.service.names).WithContext(ctx).Create(run); e != nil {
				return e
			}
			queued = true
		}
	}
	if !queued {
		return nil
	}
	return m.resultWorkChangedOn(ctx, tx, work, 0, work.State, "VERIFICATION_QUEUED", "SYSTEM:verification", row.ID, now)
}
