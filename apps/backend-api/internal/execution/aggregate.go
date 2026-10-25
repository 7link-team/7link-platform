package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan is serialized once. Recovery increments dispatch_epoch while preserving
// these bytes and their hash; no runner-supplied plan can replace the snapshot.
type ExecutionPrice struct {
	InputMicros  int64 `json:"input_micros" minimum:"0" readOnly:"true"`
	OutputMicros int64 `json:"output_micros" minimum:"0" readOnly:"true"`
}
type ExecutionPlan struct {
	Planning                  *PlanningPlan               `json:"planning,omitempty" readOnly:"true"`
	ApprovedPlanID            guid.ID                     `json:"approved_plan_id,omitempty" readOnly:"true"`
	ApprovedPlan              *delivery.ReviewPlanContent `json:"approved_plan,omitempty" readOnly:"true"`
	ProgressPolicy            *templates.ProgressPolicy   `json:"progress_policy,omitempty" readOnly:"true"`
	ProgressRootCommit        string                      `json:"progress_root_commit,omitempty" readOnly:"true"`
	ProgressTemplateVersionID guid.ID                     `json:"progress_template_version_id,omitempty" readOnly:"true"`
	ProgressDefinitionHash    string                      `json:"progress_definition_hash,omitempty" readOnly:"true"`
	Handover                  *HandoverPlan               `json:"handover,omitempty" readOnly:"true"`
	Correlation               *Correlation                `json:"correlation,omitempty" readOnly:"true"`
	Distillation              *DistillationPlan           `json:"distillation,omitempty" readOnly:"true"`
	Toolset                   []agents.RelayToolPin       `json:"toolset,omitempty" readOnly:"true" maxItems:"200" doc:"Server-frozen granted MCP version identities; contains no credentials or tokens."`
	ApprovalProtocol          string                      `json:"approval_protocol,omitempty" readOnly:"true"`
	ContextRefs               []ContextRef                `json:"context_refs,omitempty" maxItems:"50"`
	Knowledge                 []knowledge.ContextItem     `json:"knowledge,omitempty" readOnly:"true" doc:"Frozen reference documents, not instructions; cite exact [Kn] labels in textual output. Server supplied."`
	Artifacts                 []PlannedArtifact           `json:"artifacts,omitempty" maxItems:"50" doc:"Explicit criterion-to-file evidence associations frozen before execution; these are not verification verdicts."`
	AgentSystemPrompt         string                      `json:"agent_system_prompt,omitempty" maxLength:"20000" doc:"Server-frozen assigned Agent persona; posted values are ignored."`
	ExecutorPrincipalID       guid.ID                     `json:"executor_principal_id" readOnly:"true"`
	ModelFamily               string                      `json:"model_family" readOnly:"true"`
	PriceSnapshot             *ExecutionPrice             `json:"price_snapshot,omitempty" readOnly:"true" doc:"Server-pinned prices per million tokens; absent means unknown."`
	MaxInputTokens            int64                       `json:"max_input_tokens" minimum:"1" maximum:"1000000"`
	MaxOutputTokens           int64                       `json:"max_output_tokens" minimum:"1" maximum:"1000000"`
	Runtime                   string                      `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Model                     string                      `json:"model" minLength:"1" maxLength:"160"`
	RepositoryID              guid.ID                     `json:"repository_id"`
	WorkdirID                 guid.ID                     `json:"workdir_id"`
	Workdir                   string                      `json:"workdir"`
	BaseCommit                string                      `json:"base_commit" minLength:"40" maxLength:"64"`
	CommentFeedback           string                      `json:"comment_feedback,omitempty" readOnly:"true" maxLength:"16000"`
	QualityFeedback           string                      `json:"quality_feedback,omitempty" readOnly:"true" maxLength:"20000"`
	Objective                 string                      `json:"objective" minLength:"1" maxLength:"20000"`
	AuthorizationMode         string                      `json:"authorization_mode" enum:"READ_ONLY,WORKSPACE_WRITE,ON_REQUEST,FULL_AUTO"`
}
type ExecutionCreate struct {
	strategy            *strategySeed
	distillation        *knowledge.DistillationLaunch
	ensemblePlanID      guid.ID
	ensembleExecutionID guid.ID
	ProjectID           guid.ID       `json:"project_id"`
	WorkItemID          guid.ID       `json:"work_item_id"`
	AssignmentID        guid.ID       `json:"assignment_id"`
	MachineID           guid.ID       `json:"machine_id"`
	Plan                ExecutionPlan `json:"plan"`
}
type ExecutionClaimReference struct {
	ID     guid.ID `json:"id"`
	Origin string  `json:"origin" enum:"EXECUTION"`
}
type ExecutionView struct {
	Progress          *ProgressState           `json:"progress,omitempty" readOnly:"true"`
	PendingClaim      json.RawMessage          `json:"pending_claim,omitempty"`
	FullAutoGrantID   *guid.ID                 `json:"full_auto_grant_id,omitempty" readOnly:"true"`
	ParentExecutionID *guid.ID                 `json:"parent_execution_id,omitempty"`
	SourceDecisionID  *guid.ID                 `json:"source_decision_id,omitempty"`
	BranchReason      string                   `json:"branch_reason"`
	StartPoint        string                   `json:"start_point"`
	FeedbackSnapshot  string                   `json:"feedback_snapshot"`
	Autostart         bool                     `json:"autostart"`
	Claim             *ExecutionClaimReference `json:"claim,omitempty"`
	ID                guid.ID                  `json:"id"`
	ProjectID         guid.ID                  `json:"project_id"`
	WorkItemID        guid.ID                  `json:"work_item_id"`
	AssignmentID      guid.ID                  `json:"assignment_id"`
	MachineID         guid.ID                  `json:"machine_id"`
	State             string                   `json:"state"`
	StateReason       string                   `json:"state_reason"`
	ResultState       string                   `json:"result_state"`
	Plan              ExecutionPlan            `json:"plan"`
	PlanHash          string                   `json:"plan_hash"`
	DispatchEpoch     int64                    `json:"dispatch_epoch"`
	LastSeq           int64                    `json:"last_seq"`
	LeaseExpiresAt    *time.Time               `json:"lease_expires_at"`
	CancelRequestedAt *time.Time               `json:"cancel_requested_at"`
	Version           int                      `json:"version"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

func transaction(tx *gorm.DB, tenant, id guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	return nil
}
func hash(content []byte) string { return fmt.Sprintf("%x", sha256.Sum256(content)) }
func bounded(value string, n int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= n && strings.IndexFunc(value, unicode.IsControl) < 0
}
func commitHash(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func (m *Module) nextID() (guid.ID, error) {
	if m == nil || m.ids == nil {
		return 0, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(id), nil
}
func (m *Module) writer() (*gorm.DB, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}
func (m *Module) rowOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, lock bool) (*model.Execution, error) {
	if err := transaction(tx, tenant, id); err != nil {
		return nil, err
	}
	q := query.BindExecutions(tx, m.names)
	s := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if lock {
		s = s.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := s.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, err
}
func view(row *model.Execution) (ExecutionView, error) {
	var plan ExecutionPlan
	if hash([]byte(row.PlanJSON)) != row.PlanHash || json.Unmarshal([]byte(row.PlanJSON), &plan) != nil {
		return ExecutionView{}, ErrUnavailable
	}
	out := ExecutionView{ParentExecutionID: branchParent(row), SourceDecisionID: row.SourceDecisionID, BranchReason: row.BranchReason, StartPoint: row.StartPoint, FeedbackSnapshot: row.FeedbackSnapshot, Autostart: row.Autostart, ID: row.ID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, MachineID: row.MachineID, State: row.State, StateReason: row.StateReason, ResultState: row.ResultState, Plan: plan, PlanHash: row.PlanHash, DispatchEpoch: row.DispatchEpoch, LastSeq: row.LastSeq, LeaseExpiresAt: row.LeaseExpiresAt, CancelRequestedAt: row.CancelRequestedAt, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.PendingClaimJSON != nil {
		if !json.Valid([]byte(*row.PendingClaimJSON)) {
			return ExecutionView{}, ErrUnavailable
		}
		out.PendingClaim = json.RawMessage(*row.PendingClaimJSON)
	}
	if row.State == "SUCCEEDED" && row.BranchReason != "ENSEMBLE" && row.BranchReason != "SKILL_DISTILLATION" && row.BranchReason != "HANDOVER" && row.BranchReason != "PLANNING" && row.BranchReason != "PLAN_REVIEW" {
		out.Claim = &ExecutionClaimReference{ID: row.ID, Origin: "EXECUTION"}
	}
	if row.ProgressJSON != nil {
		var p ProgressState
		if json.Unmarshal([]byte(*row.ProgressJSON), &p) != nil {
			return out, ErrUnavailable
		}
		out.Progress = &p
	}
	return out, nil
}
func link(row *model.Execution, actor guid.ID) WorkLink {
	l := WorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: actor, InitiatorID: row.InitiatorID}
	if row.BranchReason == "SKILL_DISTILLATION" {
		var p ExecutionPlan
		if json.Unmarshal([]byte(row.PlanJSON), &p) == nil && p.Distillation != nil {
			l.Distillation = p.Distillation
		}
	}
	var frozen ExecutionPlan
	if json.Unmarshal([]byte(row.PlanJSON), &frozen) == nil {
		l.ReviewPlanID = frozen.ApprovedPlanID
	}
	if row.ParentExecutionID != nil {
		l.ParentExecutionID = *row.ParentExecutionID
	}
	if row.SourceDecisionID != nil {
		l.SourceDecisionID = *row.SourceDecisionID
	}
	return l
}
func (m *Module) principalLocks(ctx context.Context, tx *gorm.DB, ids ...guid.ID) error {
	if m.principals.Lock == nil {
		return ErrUnavailable
	}
	slices.Sort(ids)
	for i, id := range ids {
		if id <= 0 {
			return ErrForbidden
		}
		if i > 0 && ids[i-1] == id {
			continue
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) humanAuthority(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if m.policy == nil || m.principals.ActiveHumanByID == nil {
		return ErrUnavailable
	}
	if err := m.principalLocks(ctx, tx, actor); err != nil {
		return err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	c, err := m.policy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return ErrForbidden
	}
	return nil
}
func (m *Module) eventOn(ctx context.Context, tx *gorm.DB, row *model.Execution, kind, actorKind, authority string, detail map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	if kind == "execution.state_changed" {
		if state, ok := detail["state"].(string); ok && (state == "SUCCEEDED" || state == "FAILED" || state == StateCanceled || state == "INTERRUPTED") {
			if err := m.recordExecutionActivityOn(ctx, tx, row, "EXECUTION_ENDED", "execution", row.ID, actorKind); err != nil {
				return err
			}
			detail["activity_kind"] = "EXECUTION_ENDED"
		} else if detail["receipt_kind"] == "ACCEPTED" || detail["receipt_kind"] == "PAUSED" || detail["receipt_kind"] == "RESUMED" || detail["cancel_requested"] == true || detail["unknown_result_acknowledged"] == true || detail["control"] != nil || (detail["state"] == "CREATED" && detail["leased"] != true) {
			if err := m.recordExecutionActivityOn(ctx, tx, row, "STATE_CHANGED", "execution", row.ID, actorKind); err != nil {
				return err
			}
			detail["activity_kind"] = "STATE_CHANGED"
		}
	}
	p, err := view(row)
	if err != nil {
		return err
	}
	ctx, detail, span := traceEvent(ctx, row, p.Plan, kind, detail)
	defer span.End()
	event := audit.NewDomainEvent(ctx, row.TenantID, kind, "execution", row.ID)
	for k, v := range detail {
		event.Detail[k] = v
	}
	event.Detail["project_id"] = row.ProjectID.String()
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actorKind == "SYSTEM" {
		actor = 0
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: kind, Subject: events.Subject{Kind: "execution", ID: row.ID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: authority}, Data: detail, CorrelationID: traceID(detail)})
	return err
}

func (m *Module) CreateOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in ExecutionCreate) (ExecutionView, error) {
	if in.strategy != nil || in.ensemblePlanID != 0 || in.ensembleExecutionID != 0 {
		return ExecutionView{}, ErrForbidden
	}
	return m.createOn(ctx, tx, tenant, in)
}
func (m *Module) createOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in ExecutionCreate) (ExecutionView, error) {
	in.Plan.ProgressPolicy, in.Plan.ProgressRootCommit, in.Plan.ProgressDefinitionHash, in.Plan.ProgressTemplateVersionID = nil, "", "", 0
	in.Plan.Planning, in.Plan.ApprovedPlanID, in.Plan.ApprovedPlan = nil, 0, nil
	in.Plan.Handover = nil     // Only the internal retirement command can create this purpose.
	in.Plan.Distillation = nil // Purpose is never accepted from a posted plan.
	if in.distillation != nil {
		in.Plan.Distillation = &DistillationPlan{ID: in.distillation.DistillationID, AgentPrincipalID: in.distillation.AgentPrincipalID, SourceExecutionID: in.distillation.SourceExecutionID}
	}
	in.Plan.ApprovalProtocol = ""
	if in.Plan.AuthorizationMode == "ON_REQUEST" && in.Plan.Runtime == "CLAUDE_CODE" {
		in.Plan.ApprovalProtocol = approvalProtocol
	}
	if in.Plan.AuthorizationMode == "ON_REQUEST" && in.Plan.Runtime == "CODEX" {
		in.Plan.ApprovalProtocol = "CODEX_APP_SERVER_V1"
	}
	in.Plan.CommentFeedback = "" // Frozen from canonical Delivery feedback below.
	in.Plan.QualityFeedback = "" // Only a genuine SYSTEM source decision may set feedback.
	if err := transaction(tx, tenant, in.ProjectID); err != nil {
		return ExecutionView{}, err
	}
	if in.Plan.MaxInputTokens <= 0 || in.Plan.MaxInputTokens > 1000000 || in.Plan.MaxOutputTokens <= 0 || in.Plan.MaxOutputTokens > 1000000 || in.WorkItemID <= 0 || (in.AssignmentID <= 0 && in.distillation == nil) || (in.distillation != nil && in.AssignmentID != 0) || in.MachineID <= 0 || in.Plan.RepositoryID <= 0 || !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, in.Plan.Runtime) || !bounded(in.Plan.Model, 160) || !commitHash(in.Plan.BaseCommit) || !utf8.ValidString(in.Plan.Objective) || strings.TrimSpace(in.Plan.Objective) == "" || len(in.Plan.Objective) > 20000 || !slices.Contains([]string{"READ_ONLY", "WORKSPACE_WRITE", "ON_REQUEST", "FULL_AUTO"}, in.Plan.AuthorizationMode) {
		return ExecutionView{}, ErrInvalidArgument
	}
	// FULL_AUTO plans may be prepared, but dispatch requires a separate recorded
	// Human grant; the immutable plan itself never contains a reauthentication proof.
	if in.Plan.AuthorizationMode == "ON_REQUEST" && !validPlanApprovalProtocol(in.Plan) {
		return ExecutionView{}, ErrApprovalUnavailable
	}
	if len(in.Plan.Artifacts) > 0 && in.Plan.AuthorizationMode != "WORKSPACE_WRITE" && in.Plan.AuthorizationMode != "ON_REQUEST" && in.Plan.AuthorizationMode != "FULL_AUTO" {
		return ExecutionView{}, ErrInvalidArgument
	}
	if m.machines.ByIDOn == nil {
		return ExecutionView{}, ErrUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, in.MachineID)
	if err != nil {
		return ExecutionView{}, err
	}
	if err = m.workPrincipalLocks(ctx, tx, WorkLink{TenantID: tenant, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: in.AssignmentID, Distillation: in.Plan.Distillation}, authn.PrincipalIDFromContext(ctx), machine.PrincipalID); err != nil {
		return ExecutionView{}, err
	}
	if err := m.humanAuthority(ctx, tx, tenant, in.ProjectID, "execution_start"); err != nil {
		return ExecutionView{}, err
	}
	id, err := m.nextID()
	if err != nil {
		return ExecutionView{}, err
	}
	if in.ensemblePlanID > 0 {
		id = in.ensembleExecutionID
		if id <= 0 {
			return ExecutionView{}, ErrForbidden
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.Execution{ID: id, TenantID: tenant, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: in.AssignmentID, MachineID: in.MachineID, InitiatorID: authn.PrincipalIDFromContext(ctx), State: "CREATED", ResultState: "NONE", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	if in.Plan.Distillation != nil {
		row.BranchReason = "SKILL_DISTILLATION"
		purpose, e := json.Marshal(in.Plan)
		if e != nil {
			return ExecutionView{}, e
		}
		row.PlanJSON = string(purpose)
	}
	if in.ensemblePlanID > 0 {
		row.BranchReason = "ENSEMBLE"
	}
	if in.Plan.AuthorizationMode == "FULL_AUTO" {
		row.StateReason = "FULL_AUTO_APPROVAL_REQUIRED"
	}
	if m.work.AuthorizeOn == nil || m.machines.ResolveWorkspaceOn == nil || m.machines.ReserveSlotOn == nil {
		return ExecutionView{}, ErrCapabilityUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(row, row.InitiatorID), "CREATE"); err != nil {
		return ExecutionView{}, err
	}
	if m.commentFeedback != nil && in.distillation == nil {
		in.Plan.CommentFeedback, err = m.commentFeedback(ctx, tx, tenant, in.ProjectID, in.WorkItemID, row.InitiatorID)
		if err != nil {
			return ExecutionView{}, err
		}
	}
	if in.strategy != nil {
		if err = m.freezeStrategyOn(ctx, tx, row, &in.Plan, *in.strategy); err != nil {
			return ExecutionView{}, err
		}
	}
	if err = m.validatePlannedArtifactsOn(ctx, tx, link(row, row.InitiatorID), in.Plan.Artifacts); err != nil {
		return ExecutionView{}, err
	}
	if err = m.pinExecutionIdentityOn(ctx, tx, link(row, row.InitiatorID), &in.Plan); err != nil {
		return ExecutionView{}, err
	}
	if err = m.validateAgentBindingOn(ctx, tx, link(row, row.InitiatorID), in.MachineID, in.Plan); err != nil {
		return ExecutionView{}, err
	}
	if err = m.budgetOn(ctx, tx, tenant, link(row, row.InitiatorID), &in.Plan); err != nil {
		return ExecutionView{}, err
	}
	if in.distillation == nil {
		if err = m.pinKnowledgeOn(ctx, tx, row, &in.Plan); err != nil {
			return ExecutionView{}, err
		}
	} else {
		// ADHOC distillation has a frozen accepted-source input, rather than an
		// active work assignment for the ordinary automatic retrieval command.
		in.Plan.ContextRefs, in.Plan.Knowledge = nil, nil
	}
	workspace, err := m.machines.ResolveWorkspaceOn(ctx, tx, tenant, in.MachineID, in.ProjectID, in.Plan.RepositoryID)
	if err != nil {
		return ExecutionView{}, err
	}
	// Client paths are never trusted; resolve registered project/repository binding.
	in.Plan.WorkdirID, in.Plan.Workdir = workspace.ID, workspace.Path
	if err = m.pinProgressPolicyOn(ctx, tx, row, &in.Plan); err != nil {
		return ExecutionView{}, err
	}
	in.Plan.Correlation, err = newCorrelation(ctx, nil)
	if err != nil {
		return ExecutionView{}, err
	}
	encoded, err := json.Marshal(in.Plan)
	if err != nil {
		return ExecutionView{}, err
	}
	if len(encoded) > 65535 {
		return ExecutionView{}, ErrInvalidArgument
	}
	row.PlanJSON, row.PlanHash = string(encoded), hash(encoded)
	if _, err = m.machines.ReserveSlotOn(ctx, tx, tenant, in.MachineID, id); err != nil {
		return ExecutionView{}, err
	}
	if err = m.workspaceFreeOn(ctx, tx, tenant, in.MachineID, id, workspace.ID); err != nil {
		return ExecutionView{}, err
	}
	if err = query.BindExecutions(tx, m.names).WithContext(ctx).Create(row); err != nil {
		return ExecutionView{}, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "permission:execution_start", map[string]any{"state": "CREATED", "plan_hash": row.PlanHash}); err != nil {
		return ExecutionView{}, err
	}
	return view(row)
}
