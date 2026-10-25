package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

// ReminderWorkAccess delegates work anchor reads to the authoritative reader.
type ReminderWorkAccess struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error)
}

func (m *Module) provideReminderWork(s *module.Services) error {
	var transfer RetirementDispatch
	if err := module.Provide(s, RetirementDispatchConnector{Bind: func(provider RetirementDispatch) error {
		if provider.TransferOn == nil || transfer.TransferOn != nil {
			return ErrInvalidArgument
		}
		transfer = provider
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide(s, RetirementWork{CheckReadOn: m.checkRetirementWorkReadOn, ClearLeadsOn: m.clearRetirementLeadsOn, LeadProjectsOn: m.retirementLeadProjectsOn, InterruptExecutionOn: m.interruptRetirementExecutionOn, DecisionsOn: m.retirementDecisionsOn, PrincipalIDsOn: m.retirementPrincipalIDsOn, ListOn: m.retirementAssignmentsOn, ReleaseOn: m.releaseRetirementAssignmentOn, ReassignOn: m.reassignRetirementAssignmentOn, TransferOn: func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, assignment RetirementAssignment, in RetirementTransferInput) (guid.ID, error) {
		if in.RetirementID <= 0 || in.ArtifactID <= 0 || in.AssigneePrincipalID <= 0 || in.AssigneePrincipalID == agent || !slices.Contains([]string{"HUMAN", "AGENT"}, in.AssigneeKind) || authn.TenantIDFromContext(ctx) != tenant {
			return 0, ErrInvalidArgument
		}
		if transfer.TransferOn == nil {
			return 0, ErrUnavailable
		}
		if _, _, _, err := m.lockRetirementAssignmentOn(ctx, tx, tenant, agent, assignment, in.AssigneePrincipalID); err != nil {
			return 0, err
		}
		_, isAgent, err := m.agents.ByPrincipalOn(ctx, tx, tenant, in.AssigneePrincipalID)
		if err != nil {
			return 0, err
		}
		if isAgent != (in.AssigneeKind == "AGENT") {
			return 0, ErrInvalidMember
		}
		return transfer.TransferOn(ctx, tx, tenant, assignment, in)
	}}); err != nil {
		return err
	}
	if err := module.Provide[SkillDistillationWork](s, SkillDistillationWork{ReadOn: m.acceptedSkillSourceOn}); err != nil {
		return err
	}
	if err := module.Provide[ReminderWorkAccess](s, ReminderWorkAccess{CheckOn: m.reminderWorkOn}); err != nil {
		return err
	}
	return module.Provide[MemoryWorkSource](s, MemoryWorkSource{DecisionsOn: m.knowledgeDecisionsOn, ReadOn: func(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (string, error) {
		if _, err := m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx)); err != nil {
			return "", err
		}
		w := query.BindWorkItems(tx, m.service.names)
		row, err := w.WithContext(ctx).Select(w.RequirementID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(work)).First()
		if err != nil {
			return "", err
		}
		if row.RequirementID == nil {
			return "", nil
		}
		r := query.BindRequirements(tx, m.service.names)
		req, err := r.WithContext(ctx).Select(r.SourceIntakeID).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(*row.RequirementID)).First()
		if err != nil {
			return "", err
		}
		if req.SourceIntakeID == nil {
			return "", nil
		}
		source, err := module.Resolve[intake.MemorySourceText](s)
		if err != nil || source.ReadOn == nil {
			return "", ErrUnavailable
		}
		return source.ReadOn(ctx, tx, tenant, project, *req.SourceIntakeID)
	}})
}

// RetirementWork keeps retirement orchestration out of Delivery storage. Each
// mutation rechecks current work authority and ownership in the caller's tx.
// Reassignment requires the real proposal/decision produced by Clientwork; a
// retirement ID is never used as a fabricated dispatch proposal or decision.
type RetirementWork struct {
	CheckReadOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	ClearLeadsOn         func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	LeadProjectsOn       func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	InterruptExecutionOn func(context.Context, *gorm.DB, ExecutionWorkLink) error
	DecisionsOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]RetirementDecision, error)
	PrincipalIDsOn       func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	ListOn               func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementAssignment, error)
	ReleaseOn            func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, RetirementAssignment) error
	ReassignOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, RetirementAssignment, DispatchAssignmentInput) (guid.ID, error)
	TransferOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, RetirementAssignment, RetirementTransferInput) (guid.ID, error)
}

// Discover all accountable principals before the coordinator acquires its
// protocol lock. Source commands still recheck ownership/version under locks.
func (m *Module) retirementPrincipalIDsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]guid.ID, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	a := query.BindAssignments(tx, m.service.names)
	w := query.BindWorkItems(tx, m.service.names)
	var ids []guid.ID
	err := a.WithContext(ctx).Join(w, a.TenantID.EqCol(w.TenantID), a.ProjectID.EqCol(w.ProjectID), a.WorkItemID.EqCol(w.ID)).Select(w.AccountablePrincipalID).Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(agent), a.Status.Eq("ACTIVE"), a.Role.Eq("EXECUTOR"), w.ArchivedAt.IsNull()).Order(a.ID).Limit(1001).Scan(&ids)
	if err != nil {
		return nil, err
	}
	if len(ids) > 1000 {
		return nil, ErrUnavailable
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// RetirementTransferInput cites the genuine handover artifact validated by the
// retirement owner. Its caller owns the idempotent command transaction.
type RetirementTransferInput struct {
	RetirementID        guid.ID
	ArtifactID          guid.ID
	AssigneePrincipalID guid.ID
	AssigneeKind        string
}

// RetirementDispatch is bound by Clientwork to its proposal and decision
// commands. Delivery retains assignment validation and storage ownership.
type RetirementDispatch struct {
	TransferOn func(context.Context, *gorm.DB, guid.ID, RetirementAssignment, RetirementTransferInput) (guid.ID, error)
}

type RetirementDispatchConnector struct {
	Bind func(RetirementDispatch) error
}

type RetirementAssignment struct {
	BlockReasons      string  `json:"block_reasons"`
	BlockNote         *string `json:"block_note,omitempty"`
	AssignmentID      guid.ID `json:"assignment_id"`
	ProjectID         guid.ID `json:"project_id"`
	WorkItemID        guid.ID `json:"work_item_id"`
	WorkVersion       int     `json:"work_version"`
	AssignmentVersion int     `json:"assignment_version"`
	Title             string  `json:"title"`
	State             string  `json:"state"`
}

func retirementTransaction(tx *gorm.DB, tenant, agent guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	return nil
}

func (m *Module) retirementAssignmentsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]RetirementAssignment, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	a := query.BindAssignments(tx, m.service.names)
	w := query.BindWorkItems(tx, m.service.names)
	rows := make([]RetirementAssignment, 0)
	// One locking join reads current versions on MySQL RR, even if IAM reads
	// established an older snapshot. Do not lock assignment rows and then
	// separately acquire work row locks in the reverse mutation order.
	err := a.WithContext(ctx).Join(w, a.TenantID.EqCol(w.TenantID), a.ProjectID.EqCol(w.ProjectID), a.WorkItemID.EqCol(w.ID)).
		Select(a.ID.As("assignment_id"), a.ProjectID, a.WorkItemID, a.Version.As("assignment_version"), w.Version.As("work_version"), w.Title, w.State, w.BlockReasons, w.BlockNote).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(agent), a.Status.Eq("ACTIVE"), a.Role.Eq("EXECUTOR"), w.ArchivedAt.IsNull()).
		Order(a.ProjectID, a.WorkItemID, a.ID).Limit(1001).Scan(&rows)
	if err != nil {
		return nil, err
	}
	// Never silently truncate a precheck: it is the ownership transfer manifest.
	if len(rows) > 1000 {
		return nil, ErrUnavailable
	}
	for _, row := range rows {
		if _, err = m.readWork(ctx, tx, tenant, row.ProjectID, row.WorkItemID, authn.PrincipalIDFromContext(ctx)); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func (m *Module) lockRetirementAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in RetirementAssignment, additional ...guid.ID) (*model.WorkItem, *model.Assignment, bool, error) {
	return m.lockRetirementAssignmentStateOn(ctx, tx, tenant, agent, in, false, additional...)
}

func (m *Module) lockRetirementAssignmentStateOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in RetirementAssignment, allowRetired bool, additional ...guid.ID) (*model.WorkItem, *model.Assignment, bool, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return nil, nil, false, err
	}
	if in.AssignmentID <= 0 || in.WorkItemID <= 0 || in.ProjectID <= 0 || in.WorkVersion < 1 || in.AssignmentVersion < 1 {
		return nil, nil, false, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return nil, nil, false, ErrUnavailable
	}
	if m.agents.ByPrincipalOn == nil || m.principals.Lock == nil {
		return nil, nil, false, ErrUnavailable
	}
	w := query.BindWorkItems(tx, m.service.names)
	hint, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(in.ProjectID), w.ID.Eq(in.WorkItemID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return nil, nil, false, err
	}
	principals := append([]guid.ID{authn.PrincipalIDFromContext(ctx), agent, hint.AccountablePrincipalID}, additional...)
	slices.Sort(principals)
	for _, id := range slices.Compact(principals) {
		if id <= 0 {
			return nil, nil, false, ErrInvalidArgument
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return nil, nil, false, err
		}
	}
	identity, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return nil, nil, false, err
	}
	if !found || (identity.State != "RETIRING" && !(allowRetired && identity.State == "RETIRED")) {
		return nil, nil, false, ErrAssignmentState
	}
	_, work, ownerActive, err := m.lockedWorkOn(ctx, tx, tenant, in.ProjectID, in.WorkItemID, authn.PrincipalIDFromContext(ctx), hint.AccountablePrincipalID, "work_item_assign")
	if err != nil {
		return nil, nil, false, err
	}
	a := query.BindAssignments(tx, m.service.names)
	row, err := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(in.ProjectID), a.WorkItemID.Eq(in.WorkItemID), a.ID.Eq(in.AssignmentID)).First()
	if err != nil {
		return nil, nil, false, err
	}
	terminalBranch := allowRetired && identity.State == "RETIRED" && row.EnsemblePlanID != nil
	if row.Status != "ACTIVE" || row.AssigneeKind != "AGENT" || row.AssigneePrincipalID != agent || row.Role != "EXECUTOR" || (row.EnsemblePlanID != nil && !terminalBranch) || row.Version != in.AssignmentVersion || work.Version != in.WorkVersion {
		return nil, nil, false, ErrWorkVersion
	}
	if !terminalBranch && (work.CurrentExecutionID != nil || (!(allowRetired && identity.State == "RETIRED" && work.CurrentClaimID != nil) && (work.CurrentClaimID != nil || !slices.Contains([]string{"CLAIMED", "IN_PROGRESS", "EXECUTION_FAILED"}, work.State)))) {
		return nil, nil, false, ErrAssignmentState
	}
	return work, row, ownerActive, nil
}

func (m *Module) reassignRetirementAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in RetirementAssignment, command DispatchAssignmentInput) (guid.ID, error) {
	if command.Auto || !command.Reassign || command.ProposalID <= 0 || command.DecisionID <= 0 || command.WorkItemID != in.WorkItemID || command.ExpectedWorkVersion != in.WorkVersion || command.EnsemblePlanID != 0 {
		return 0, ErrInvalidArgument
	}
	if _, _, _, err := m.lockRetirementAssignmentOn(ctx, tx, tenant, agent, in, command.AssigneePrincipalID); err != nil {
		return 0, err
	}
	return (clientWork{m: m}).dispatchApplyOn(ctx, tx, tenant, in.ProjectID, command)
}

func (m *Module) releaseRetirementAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID, in RetirementAssignment) error {
	if retirement <= 0 {
		return ErrInvalidArgument
	}
	work, row, ownerActive, err := m.lockRetirementAssignmentStateOn(ctx, tx, tenant, agent, in, true)
	if err != nil {
		return err
	}
	if m.ids == nil || m.service.audit == nil || m.service.events == nil {
		return ErrUnavailable
	}
	// Release is an operator assignment decision, not self-release under the
	// retiring Agent's credentials. AuthorityChecker must approve it now.
	source, err := (clientWork{m: m}).dispatchAuthorityOn(ctx, tx, tenant, in.ProjectID, in.WorkItemID, agent)
	if err != nil {
		return err
	}
	issues, blocks, err := m.evaluatePreparationOn(ctx, tx, work, ownerActive)
	if err != nil {
		return err
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	actor := authn.PrincipalIDFromContext(ctx)
	reason := "AGENT_RETIREMENT:" + retirement.String()
	a := query.BindAssignments(tx, m.service.names)
	changed, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ID.Eq(row.ID), a.Version.Eq(in.AssignmentVersion), a.Status.Eq("ACTIVE")).Updates(map[string]any{"status": "RELEASED", "active_slot": nil, "lease_expires_at": nil, "ended_at": at, "end_reason": reason, "updated_at": at, "updated_by": actor, "version": row.Version + 1})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	row.Status, row.ActiveSlot, row.LeaseExpiresAt, row.EndedAt, row.EndReason = "RELEASED", nil, nil, &at, &reason
	before := work.State
	if work.CurrentClaimID != nil || row.EnsemblePlanID != nil {
		// Retiring the producer does not withdraw submitted evidence or reset
		// pending/accepted review. End ownership while retaining that result.
		return m.assignmentWorkChangedByOn(ctx, tx, work, row, actor, "HUMAN", source, before, reason, at)
	}
	work.ReadinessOK = len(issues) == 0
	encoded, err := json.Marshal(issues)
	if err != nil {
		return err
	}
	work.ReadinessIssues = string(encoded)
	encoded, err = json.Marshal(blocks)
	if err != nil {
		return err
	}
	work.BlockReasons = string(encoded)
	switch {
	case len(blocks) > 0:
		work.State = "BLOCKED"
	case !work.ReadinessOK:
		work.State = "DRAFT"
	default:
		work.State = "READY"
	}
	return m.assignmentWorkChangedByOn(ctx, tx, work, row, actor, "HUMAN", source, before, reason, at)
}

func (m *Module) bindKnowledgeResults(s *module.Services) error {
	if connector, err := module.Resolve[knowledge.DistillationConnector](s); err == nil {
		if err = connector.BindSource(knowledge.DistillationSource{ReadOn: m.acceptedSkillSourceOn}); err != nil {
			return err
		}
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindKnowledgeDecisionView(s); err != nil {
		return err
	}
	connector, err := module.Resolve[knowledge.ResultExecutionConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(knowledge.ResultExecutionReader{ReadOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
		if m == nil || m.service == nil || tx == nil {
			return 0, knowledge.ErrUnavailable
		}
		r := query.BindResultVersions(tx, m.service.names)
		row, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ID.Eq(id)).First()
		if err != nil {
			return 0, err
		}
		if _, err = m.readWork(ctx, tx, tenant, row.ProjectID, row.WorkItemID, authn.PrincipalIDFromContext(ctx)); err != nil {
			return 0, err
		}
		if row.SourceExecutionID == nil {
			return 0, nil
		}
		return *row.SourceExecutionID, nil
	}})
}

// SkillDistillationWork reads accepted source facts without creating an
// assignment, changing a work pointer, or submitting a completion claim.
type SkillDistillationWork struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (knowledge.AcceptedSkillSource, error)
}

func (m *Module) acceptedSkillSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (knowledge.AcceptedSkillSource, error) {
	var out knowledge.AcceptedSkillSource
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, knowledge.ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, knowledge.ErrInvalid
	}
	q := query.BindWorkItems(tx, m.service.names)
	identity, err := q.WithContext(ctx).Select(q.ProjectID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return out, knowledgeViewError(err)
	}
	if _, err = m.workKnowledgeContextOn(ctx, tx, tenant, identity.ProjectID, id); err != nil {
		return out, knowledgeViewError(err)
	}
	actor := authn.PrincipalIDFromContext(ctx)
	constraint, err := m.workPolicy(ctx, tx, tenant, identity.ProjectID, "knowledge_write", actor)
	if err != nil {
		return out, knowledgeViewError(err)
	}
	scope, err := workScope(constraint, identity.ProjectID, q.CreatedBy)
	if err != nil {
		return out, knowledgeViewError(err)
	}
	read := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(identity.ProjectID), q.ID.Eq(id), q.ArchivedAt.IsNull())
	if scope != nil {
		read = read.Where(scope)
	}
	work, err := read.First()
	if err != nil {
		return out, knowledgeViewError(err)
	}
	if work.State != "ACCEPTED" || work.CurrentClaimID == nil {
		return out, knowledge.ErrDistillationNotAccepted
	}
	r := query.BindResultVersions(tx, m.service.names)
	result, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(work.ProjectID), r.WorkItemID.Eq(id), r.ID.Eq(*work.CurrentClaimID)).First()
	if err != nil {
		return out, knowledgeViewError(err)
	}
	if result.SourceExecutionID == nil {
		return out, knowledge.ErrUnavailable
	}
	a := query.BindResultActions(tx, m.service.names)
	action, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(work.ProjectID), a.WorkItemID.Eq(id), a.ResultID.Eq(result.ID), a.Kind.Eq("ACCEPT")).Order(a.ID.Desc()).First()
	if err != nil {
		return out, knowledgeViewError(err)
	}
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ProjectID.Eq(work.ProjectID), e.ResultID.Eq(result.ID)).Order(e.ID).Limit(101).Find()
	if err != nil {
		return out, err
	}
	if len(evidence) > 100 {
		return out, knowledge.ErrUnavailable
	}
	v := query.BindVerificationRuns(tx, m.service.names)
	passed, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ProjectID.Eq(work.ProjectID), v.ResultID.Eq(result.ID), v.State.Eq("COMPLETED"), v.Verdict.Eq("PASSED")).Order(v.ID).Limit(101).Find()
	if err != nil {
		return out, err
	}
	if len(passed) > 100 {
		return out, knowledge.ErrUnavailable
	}
	var text strings.Builder
	fmt.Fprintf(&text, "# %s\n\n## Declaration summary\n\n%s\n\n## Evidence summary\n", work.Title, result.Summary)
	for _, item := range evidence {
		fmt.Fprintf(&text, "- [%s] %s (%s)\n", item.ID, item.Name, item.Coverage)
	}
	fmt.Fprintf(&text, "\n## Accepted decision\nDecision %s; result %s\n\n## Passed verification\n", action.ID, result.ID)
	for _, item := range passed {
		fmt.Fprintf(&text, "- Run %s; criterion %s; method %s; verdict PASSED; %s\n", item.ID, item.CriterionID, item.Method, item.Reason)
	}
	return knowledge.AcceptedSkillSource{ProjectID: work.ProjectID, WorkItemID: id, ResultID: result.ID, ExecutionID: *result.SourceExecutionID, Content: text.String(), HeadCommit: result.HeadCommit}, nil
}

// MemoryWorkSource follows the actual work/requirement/source relationship.
// Empty content means that this work has no external intake source.
type MemoryWorkSource struct {
	ReadOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (string, error)
	DecisionsOn func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]KnowledgeDecisionFact, error)
}

type KnowledgeDecisionFact struct {
	ExecutionID guid.ID
	EvidenceID  guid.ID
	ProjectID   guid.ID
	Kind        string
	CreatedAt   time.Time
}

// knowledgeDecisionsOn exposes source-owned immutable acceptance decisions. The
// metrics owner intersects these with current reads and genuine citation receipts.
func (m *Module) knowledgeDecisionsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, project *guid.ID, from, to time.Time) ([]KnowledgeDecisionFact, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || !from.Before(to) {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	a, r := query.BindResultActions(tx, m.service.names), query.BindResultVersions(tx, m.service.names)
	read := a.WithContext(ctx).Select(a.ID.As("evidence_id"), a.ProjectID, a.Kind, a.CreatedAt, r.SourceExecutionID.As("execution_id")).Join(r, a.TenantID.EqCol(r.TenantID), a.ProjectID.EqCol(r.ProjectID), a.ResultID.EqCol(r.ID)).Where(a.TenantID.Eq(tenant), a.Kind.In("ACCEPT", "RETURN"), a.CreatedAt.Gte(from), a.CreatedAt.Lt(to), r.SourceExecutionID.IsNotNull())
	if project != nil {
		read = read.Where(a.ProjectID.Eq(*project))
	}
	var rows []KnowledgeDecisionFact
	if err := read.Order(a.ID).Limit(10001).Scan(&rows); err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, ErrUnavailable
	}
	return rows, nil
}
func (m *Module) reminderWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, work, actor guid.ID) (int64, error) {
	if tenant <= 0 || project <= 0 || work <= 0 || actor <= 0 {
		return 0, ErrWorkForbidden
	}
	view, err := m.readWork(ctx, tx, tenant, project, work, actor)
	if err != nil {
		return 0, err
	}
	return view.ActivitySeq, nil
}

func (m *Module) bindKnowledgeDecisionView(s *module.Services) error {
	c, err := module.Resolve[knowledge.ExecutionKnowledgeViewConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.BindDecision(func(ctx context.Context, tx *gorm.DB, tenant, project, work, id guid.ID) (knowledge.FeedbackDecisionMetadata, error) {
		out := knowledge.FeedbackDecisionMetadata{}
		if _, e := m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx)); e != nil {
			return out, knowledgeViewError(e)
		}
		a := query.BindResultActions(tx, m.service.names)
		row, e := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(work), a.ID.Eq(id)).First()
		if e != nil {
			return out, knowledgeViewError(e)
		}
		out.ActorID, out.CreatedAt = row.ActorID, row.CreatedAt
		return out, nil
	})
}

func knowledgeViewError(err error) error {
	switch {
	case errors.Is(err, ErrProjectForbidden), errors.Is(err, ErrWorkForbidden):
		return knowledge.ErrForbidden
	case errors.Is(err, ErrProjectNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return knowledge.ErrNotFound
	case errors.Is(err, ErrUnavailable):
		return knowledge.ErrUnavailable
	}
	return err
}

// AgentInboxReference contains source-owned references only. The downstream
// inbox checks current Agent identity; no message or feedback body is copied.
type AgentInboxReference struct {
	TenantID, PrincipalID, SubjectID guid.ID
	SourceEventID, Kind, StreamKey   string
	StreamSeq                        int64
}
type AgentInboxConnector struct {
	Bind func(func(context.Context, *gorm.DB, AgentInboxReference) error) error
}
type inboxPublisher struct {
	events.Publisher
	project func(context.Context, *gorm.DB, events.Event, guid.ID) error
}

func (p inboxPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	id, err := p.Publisher.Publish(ctx, tx, e)
	if err != nil {
		return 0, err
	}
	if err = p.project(ctx, tx, e, id); err != nil {
		return 0, err
	}
	return id, nil
}

// Binding happens after Delivery injection, when Agents resolves its declared
// dependency. Projection remains in the original command's transaction.
func (m *Module) provideRetirementKnowledge(s *module.Services) error {
	bound := false
	return module.Provide(s, AgentInboxConnector{Bind: func(appendOn func(context.Context, *gorm.DB, AgentInboxReference) error) error {
		if appendOn == nil || bound || m.service == nil || m.service.events == nil {
			return ErrUnavailable
		}
		bound = true
		m.service.events = inboxPublisher{Publisher: m.service.events, project: func(ctx context.Context, tx *gorm.DB, e events.Event, id guid.ID) error {
			return m.projectAgentInboxOn(ctx, tx, e, id, appendOn)
		}}
		return nil
	}})
}

func (m *Module) projectAgentInboxOn(ctx context.Context, tx *gorm.DB, e events.Event, eventID guid.ID, appendOn func(context.Context, *gorm.DB, AgentInboxReference) error) error {
	if !slices.Contains([]string{"work_item.assigned", "work_item.state_changed", "result.feedback_added"}, e.Type) {
		return nil
	}
	if tx == nil || tx.Statement == nil || eventID <= 0 || appendOn == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	data := struct {
		AssignmentID string `json:"assignment_id"`
		ResultID     string `json:"result_id"`
		Reason       string `json:"reason_code"`
		Kind         string `json:"kind"`
	}{}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	workID, resultID, target := e.Subject.ID, guid.ID(0), guid.ID(0)
	kind := "DISPATCH"
	if e.Type == "work_item.assigned" {
		assignment, err := guid.RequireID(data.AssignmentID)
		if err != nil {
			return ErrInvalidArgument
		}
		q := query.BindAssignments(tx, m.service.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProjectID.Eq(e.ProjectID), q.WorkItemID.Eq(workID), q.ID.Eq(assignment), q.Status.Eq("ACTIVE")).First()
		if err != nil {
			return err
		}
		if r.AssigneeKind != "AGENT" {
			return nil
		}
		target = r.AssigneePrincipalID
	} else {
		kind = "REVIEW_FEEDBACK"
		if e.Type == "result.feedback_added" {
			if data.Kind != "FEEDBACK" {
				return nil
			}
			resultID = e.Subject.ID
		} else {
			if !slices.Contains([]string{"RESULT_CHECKED", "RESULT_ACCEPTED", "RESULT_RETURNED", "RESULT_REJECTED", "RESULT_CHANGES_REQUESTED", "RESULT_REQUEST_CHANGES", "QUALITY_REJECTION", "SCOPE_REQUEST_CHANGES", "INTERNAL_QA_FAILED"}, data.Reason) {
				return nil
			}
			resultID, err = guid.RequireID(data.ResultID)
			if err != nil {
				return ErrInvalidArgument
			}
		}
		q := query.BindResultVersions(tx, m.service.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProjectID.Eq(e.ProjectID), q.ID.Eq(resultID)).First()
		if err != nil {
			return err
		}
		if e.Type == "work_item.state_changed" && r.WorkItemID != workID {
			return ErrInvalidArgument
		}
		workID, target = r.WorkItemID, r.ActorID
	}
	// A Human result author or assignee never becomes an Agent implicitly.
	if m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, e.TenantID, target)
	if err != nil {
		return err
	}
	if !found || !a.AcceptsInboxEvents() {
		return nil
	}
	if e.Actor.Kind == "AGENT" && e.Actor.ID == target {
		return nil
	}
	if m.workPolicy == nil {
		return ErrUnavailable
	}
	access, err := m.workPolicy(ctx, tx, e.TenantID, e.ProjectID, "work_item_read", target)
	if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectForbidden) {
		return nil
	}
	if err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(access, e.ProjectID, w.CreatedBy)
	if err != nil {
		return err
	}
	visible := w.WithContext(ctx).Where(w.TenantID.Eq(e.TenantID), w.ProjectID.Eq(e.ProjectID), w.ID.Eq(workID), w.ArchivedAt.IsNull())
	if scope != nil {
		visible = visible.Where(scope)
	}
	work, err := visible.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	subject := workID
	if resultID > 0 {
		subject = resultID
	}
	return appendOn(ctx, tx, AgentInboxReference{TenantID: e.TenantID, PrincipalID: target, SubjectID: subject, SourceEventID: eventID.String(), Kind: kind, StreamKey: "work_item:" + workID.String(), StreamSeq: work.ActivitySeq})
}
