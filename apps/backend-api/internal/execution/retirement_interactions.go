package execution

import (
	activityi18n "7link.com/platform/internal/execution/i18n"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// DistillationPlan is an immutable server-only ADHOC purpose. AssignmentID=0
// means no work assignment; accepted source work is never reopened or claimed.
type DistillationPlan struct {
	ID                guid.ID `json:"id"`
	AgentPrincipalID  guid.ID `json:"agent_principal_id"`
	SourceExecutionID guid.ID `json:"source_execution_id"`
}

func (m *Module) bindSkillDistillation(s *module.Services) error {
	if err := m.bindRetirementPrecheck(s); err != nil {
		return err
	}
	if err := m.bindInteractionInbox(s); err != nil {
		return err
	}
	c, err := module.Resolve[knowledge.DistillationConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	work, err := module.Resolve[delivery.SkillDistillationWork](s)
	if err != nil {
		return err
	}
	if work.ReadOn == nil || c.BindRuntime == nil || c.CompleteOn == nil {
		return ErrCapabilityUnavailable
	}
	original := m.work
	authorize := func(ctx context.Context, tx *gorm.DB, l WorkLink) error {
		if l.Distillation == nil || l.AssignmentID != 0 || l.Distillation.ID <= 0 || l.Distillation.AgentPrincipalID <= 0 || l.InitiatorID <= 0 {
			return ErrForbidden
		}
		principalCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: l.TenantID, PrincipalID: l.InitiatorID, SubjectType: authn.SubjectTypePrincipal})
		if err := m.humanAuthority(principalCtx, tx, l.TenantID, l.ProjectID, "knowledge_write"); err != nil {
			return err
		}
		fact, e := work.ReadOn(principalCtx, tx, l.TenantID, l.WorkItemID)
		if e != nil {
			switch {
			case errors.Is(e, knowledge.ErrForbidden):
				return ErrForbidden
			case errors.Is(e, knowledge.ErrNotFound):
				return ErrNotFound
			case errors.Is(e, knowledge.ErrUnavailable):
				return ErrCapabilityUnavailable
			case errors.Is(e, knowledge.ErrInvalid):
				return ErrConflict
			}
			return e
		}
		if fact.ProjectID != l.ProjectID || fact.ExecutionID != l.Distillation.SourceExecutionID {
			return ErrConflict
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, l.Distillation.AgentPrincipalID)
		if e != nil {
			return e
		}
		if !found || agent.State != "ACTIVE" {
			return ErrForbidden
		}
		return nil
	}
	m.work.PrincipalsOn = func(ctx context.Context, tx *gorm.DB, l WorkLink) ([]guid.ID, error) {
		if l.Distillation != nil {
			return []guid.ID{l.Distillation.AgentPrincipalID}, nil
		}
		if original.PrincipalsOn == nil {
			return nil, ErrCapabilityUnavailable
		}
		return original.PrincipalsOn(ctx, tx, l)
	}
	m.work.AssignedAgentOn = func(ctx context.Context, tx *gorm.DB, l WorkLink) (guid.ID, error) {
		if l.Distillation != nil {
			return l.Distillation.AgentPrincipalID, nil
		}
		if original.AssignedAgentOn == nil {
			return 0, ErrCapabilityUnavailable
		}
		return original.AssignedAgentOn(ctx, tx, l)
	}
	m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
		if l.Distillation != nil {
			if !slices.Contains([]string{"CREATE", "DISPATCH", "RECEIPT", "CONTROL_RECEIPT", "CANCEL", "RECOVER"}, op) {
				return ErrInvalidArgument
			}
			return authorize(ctx, tx, l)
		}
		if original.AuthorizeOn == nil {
			return ErrCapabilityUnavailable
		}
		return original.AuthorizeOn(ctx, tx, l, op)
	}
	m.work.StartedOn = func(ctx context.Context, tx *gorm.DB, l WorkLink) error {
		if l.Distillation != nil {
			return authorize(ctx, tx, l)
		}
		if original.StartedOn == nil {
			return ErrCapabilityUnavailable
		}
		return original.StartedOn(ctx, tx, l)
	}
	m.work.FinishedOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, result WorkResult) error {
		if l.Distillation != nil {
			if err := authorize(ctx, tx, l); err != nil {
				return err
			}
			if original.SettleBudgetOn == nil {
				return ErrCapabilityUnavailable
			}
			return original.SettleBudgetOn(ctx, tx, l, result)
		}
		if original.FinishedOn == nil {
			return ErrCapabilityUnavailable
		}
		return original.FinishedOn(ctx, tx, l, result)
	}
	m.work.DistillationCompletedOn = c.CompleteOn
	return c.BindRuntime(knowledge.DistillationRuntime{PrincipalsOn: func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]guid.ID, error) {
		ids, e := m.skillDistillationPrincipalsOn(ctx, tx, tenant, agent)
		return ids, distillationKnowledgeError(e)
	}, LaunchOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, in knowledge.DistillationLaunch) (guid.ID, error) {
		id, e := m.launchSkillDistillationOn(ctx, tx, tenant, in)
		return id, distillationKnowledgeError(e)
	}, ReadOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (knowledge.DistillationOutput, error) {
		value, e := m.skillDistillationOutputOn(ctx, tx, tenant, id)
		return value, distillationKnowledgeError(e)
	}})
}

func (m *Module) bindRetirementPrecheck(s *module.Services) error {
	if err := m.bindHandover(s); err != nil {
		return err
	}
	c, err := module.Resolve[agents.RetirementExecutionConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrCapabilityUnavailable
	}
	return c.Bind(func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]agents.RetirementExecution, error) {
		out, e := m.retirementExecutionsOn(ctx, tx, tenant, agent)
		switch {
		case errors.Is(e, ErrForbidden):
			return nil, agents.ErrForbidden
		case errors.Is(e, ErrUnavailable), errors.Is(e, ErrCapabilityUnavailable):
			return nil, agents.ErrUnavailable
		default:
			return out, e
		}
	})
}

func (m *Module) retirementExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]agents.RetirementExecution, error) {
	if transaction(tx, tenant, agent) != nil {
		return nil, ErrInvalidArgument
	}
	if err := m.principalLocks(ctx, tx, authn.PrincipalIDFromContext(ctx), agent); err != nil {
		return nil, err
	}
	rows, err := m.retirementExecutionRowsOn(ctx, tx, tenant, agent)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, ErrCapabilityUnavailable
	}
	out := make([]agents.RetirementExecution, 0)
	for _, row := range rows {
		v, e := view(row)
		if e != nil {
			return nil, e
		}
		if v.Plan.ExecutorPrincipalID != agent || v.Plan.Handover != nil {
			continue
		}
		if e = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); e != nil {
			return nil, e
		}
		out = append(out, agents.RetirementExecution{ID: row.ID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, State: row.State, Version: row.Version})
	}
	return out, nil
}

type interactionPublisher struct {
	events.Publisher
	m     *Module
	inbox agents.InboxAppender
}

func (p interactionPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	id, err := p.Publisher.Publish(ctx, tx, e)
	if err != nil {
		return 0, err
	}
	if e.Type == "execution.artifact_comment_added" {
		return id, p.projectArtifactCommentOn(ctx, tx, e, id)
	}
	if e.Type == "execution.evidence_recorded" {
		row, readErr := p.m.rowOn(ctx, tx, e.TenantID, e.Subject.ID, false)
		if readErr != nil {
			return 0, readErr
		}
		return id, p.projectExecutionInboxOn(ctx, tx, row, e.Actor.ID, agents.InboxReference{SourceEventID: id.String(), Kind: "EXECUTION", SubjectID: row.ID})
	}
	if e.Type != "execution.state_changed" {
		return id, nil
	}
	data := struct {
		ApprovalID    string `json:"approval_id"`
		ApprovalState string `json:"approval_state"`
		ActivityKind  string `json:"activity_kind"`
	}{}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return 0, err
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return 0, err
	}
	row, err := p.m.rowOn(ctx, tx, e.TenantID, e.Subject.ID, false)
	if err != nil {
		return 0, err
	}
	if data.ApprovalID != "" && data.ApprovalState != "PENDING" {
		approval, err := guid.RequireID(data.ApprovalID)
		if err != nil {
			return 0, ErrInvalidArgument
		}
		q := query.BindApprovals(tx, p.m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ExecutionID.Eq(row.ID), q.ID.Eq(approval)).First()
		if err != nil {
			return 0, err
		}
		if r.State != data.ApprovalState || !slices.Contains([]string{"ALLOW", "DENY"}, r.State) {
			return 0, ErrConflict
		}
		if err = p.projectInteractionOn(ctx, tx, row, r, e.Actor.ID); err != nil {
			return 0, err
		}
	}
	if row.CancelRequestedAt != nil || !active(row.State) {
		q := query.BindApprovals(tx, p.m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ExecutionID.Eq(row.ID), q.State.Eq("PENDING")).Order(q.ID).Find()
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if err = p.projectInteractionOn(ctx, tx, row, r, e.Actor.ID); err != nil {
				return 0, err
			}
		}
	}
	if data.ActivityKind != "" {
		if err = p.projectExecutionInboxOn(ctx, tx, row, e.Actor.ID, agents.InboxReference{SourceEventID: id.String(), Kind: "EXECUTION", SubjectID: row.ID}); err != nil {
			return 0, err
		}
	}
	return id, nil
}
func (m *Module) bindInteractionInbox(s *module.Services) error {
	inbox, err := module.Resolve[agents.InboxAppender](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if inbox.AppendOn == nil || m.events == nil {
		return ErrCapabilityUnavailable
	}
	if _, bound := m.events.(interactionPublisher); bound {
		return ErrConflict
	}
	m.events = interactionPublisher{Publisher: m.events, m: m, inbox: inbox}
	return m.registerInteractionExpiry(s)
}

func (m *Module) registerInteractionExpiry(s *module.Services) error {
	catalog, err := module.Resolve[jobs.Catalog](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	const kind = "execution.interaction_inbox_expiry"
	if err = catalog.AddHandlers(Name, jobs.Handler{DisplayNameKey: activityi18n.Namespace + ".activity_job_execution_interaction_inbox_expiry_name", DescriptionKey: activityi18n.Namespace + ".activity_job_execution_interaction_inbox_expiry_description", Kind: kind, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		db, err := m.writer()
		if err != nil {
			return err
		}
		return m.expireInteractions(ctx, db, job.TenantID, time.Now().UTC())
	}}); err != nil {
		return err
	}
	return catalog.AddSchedules(Name, jobs.Schedule{Name: kind, Kind: kind, Interval: 5 * time.Second, TenantScope: jobs.ScopePerTenant})
}
func (p interactionPublisher) projectInteractionOn(ctx context.Context, tx *gorm.DB, row *model.Execution, r *model.Approval, actor guid.ID) error {
	if r.TenantID != row.TenantID || r.ExecutionID != row.ID {
		return ErrConflict
	}
	return p.projectExecutionInboxOn(ctx, tx, row, actor, agents.InboxReference{SourceEventID: "interaction:" + r.ID.String() + ":terminal", Kind: "INTERACTION", SubjectID: r.ID})
}

func (p interactionPublisher) projectArtifactCommentOn(ctx context.Context, tx *gorm.DB, e events.Event, eventID guid.ID) error {
	data := struct {
		ThreadID  string `json:"thread_id"`
		MessageID string `json:"message_id"`
		ThreadSeq int64  `json:"thread_seq"`
	}{}
	raw, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	id, err := guid.RequireID(data.ThreadID)
	if err != nil {
		return ErrInvalidArgument
	}
	q := query.Use(tx).ArtifactCommentThread.Table(p.m.names.CommentThreads.String())
	thread, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ID.Eq(id), q.ExecutionID.Eq(e.Subject.ID), q.ProjectID.Eq(e.ProjectID)).First()
	if err != nil {
		return err
	}
	row, err := p.m.rowOn(ctx, tx, e.TenantID, thread.ExecutionID, false)
	if err != nil {
		return err
	}
	v, err := view(row)
	if err != nil {
		return err
	}
	actor := thread.CreatedBy
	if data.MessageID != "" {
		messageID, parseErr := guid.RequireID(data.MessageID)
		if parseErr != nil {
			return ErrInvalidArgument
		}
		mq := query.BindArtifactCommentMessages(tx, p.m.names)
		message, readErr := mq.WithContext(ctx).Where(mq.TenantID.Eq(e.TenantID), mq.ThreadID.Eq(thread.ID), mq.ID.Eq(messageID), mq.Seq.Eq(data.ThreadSeq)).First()
		if readErr != nil {
			return readErr
		}
		actor = message.CreatedBy
	}
	if actor != e.Actor.ID || row.ProjectID != thread.ProjectID || row.WorkItemID != thread.WorkItemID {
		return ErrConflict
	}
	if actor == v.Plan.ExecutorPrincipalID {
		return nil
	}
	ref := agents.InboxReference{SourceEventID: eventID.String(), Kind: "MESSAGE", SubjectID: thread.ID}
	if data.MessageID != "" {
		ref.StreamKey, ref.StreamSeq = "comment_thread:"+thread.ID.String(), data.ThreadSeq
	}
	return p.projectExecutionInboxOn(ctx, tx, row, actor, ref)
}

func (p interactionPublisher) projectExecutionInboxOn(ctx context.Context, tx *gorm.DB, row *model.Execution, actor guid.ID, ref agents.InboxReference) error {
	if hash([]byte(row.PlanJSON)) != row.PlanHash {
		return ErrConflict
	}
	v, err := view(row)
	if err != nil {
		return err
	}
	// HANDOVER has no project or work activity stream. Its machine transport
	// and retirement protocol carry its lifecycle; project read policy cannot
	// authorize an inbox source for project zero.
	if v.Plan.Handover != nil && row.ProjectID == 0 {
		return nil
	}
	target := v.Plan.ExecutorPrincipalID
	if target <= 0 || actor == target {
		return nil
	}
	if p.m.agents.ByPrincipalOn == nil {
		return ErrCapabilityUnavailable
	}
	a, found, err := p.m.agents.ByPrincipalOn(ctx, tx, row.TenantID, target)
	if err != nil {
		return err
	}
	if !found || !a.AcceptsInboxEvents() {
		return nil
	}
	if p.m.policy == nil {
		return ErrCapabilityUnavailable
	}
	access, err := p.m.policy(ctx, tx, row.TenantID, row.ProjectID, "execution_read", target)
	if errors.Is(err, ErrForbidden) {
		return nil
	}
	if err != nil {
		return err
	}
	if !access.AllowsEntityRows(row.ProjectID) || (!access.AllowAll && !slices.Contains(access.ResourceIDs, row.ProjectID.String()) && !slices.Contains(access.OwnerIDs, target.String())) {
		return nil
	}
	if row.WorkItemID > 0 && ref.StreamKey == "" {
		if p.m.commentContext.HeadOn == nil {
			return ErrCapabilityUnavailable
		}
		head, e := p.m.commentContext.HeadOn(ctx, tx, row.TenantID, row.WorkItemID)
		if e != nil {
			return e
		}
		ref.StreamKey, ref.StreamSeq = "work_item:"+row.WorkItemID.String(), head
	}
	_, err = p.inbox.AppendOn(ctx, tx, row.TenantID, target, ref)
	return err
}

// Expiration is a source fact derived from the persisted deadline. It neither
// claims a runtime response nor changes a finished execution. The existing
// tenant-scoped expiry job calls it; the stable source identity prevents repeated
// wake receipts.
func (m *Module) expireInteractions(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	p, enabled := m.events.(interactionPublisher)
	if !enabled {
		return nil
	}
	q := query.BindApprovals(db, m.names)
	var cursor guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("PENDING"), q.ExpiresAt.Lte(now), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, snapshot := range rows {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				row, err := m.rowOn(ctx, tx, tenant, snapshot.ExecutionID, false)
				if err != nil {
					return err
				}
				v, err := view(row)
				if err != nil {
					return err
				}
				if v.Plan.ExecutorPrincipalID <= 0 {
					return nil
				}
				if m.principals.Lock == nil {
					return ErrCapabilityUnavailable
				}
				if err = m.principals.Lock(ctx, tx, v.Plan.ExecutorPrincipalID); err != nil {
					return err
				}
				row, err = m.rowOn(ctx, tx, tenant, row.ID, true)
				if err != nil {
					return err
				}
				a := query.BindApprovals(tx, m.names)
				r, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ID.Eq(snapshot.ID)).First()
				if err != nil {
					return err
				}
				if r.State != "PENDING" || r.ExpiresAt.After(now) {
					return nil
				}
				return p.projectInteractionOn(ctx, tx, row, r, 0)
			})
			if err != nil {
				return err
			}
			cursor = snapshot.ID
		}
	}
}

func distillationKnowledgeError(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return knowledge.ErrForbidden
	case errors.Is(err, ErrNotFound):
		return knowledge.ErrNotFound
	case errors.Is(err, ErrInvalidArgument):
		return knowledge.ErrInvalid
	case errors.Is(err, ErrConflict), errors.Is(err, ErrBudgetExceeded):
		return knowledge.ErrConflict
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrCapabilityUnavailable):
		return knowledge.ErrUnavailable
	}
	return err
}

func (m *Module) skillDistillationPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]guid.ID, error) {
	if m.agentRuntime.ByPrincipalOn == nil || m.machines.ByIDOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	runtime, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return nil, err
	}
	if !found || runtime.MachineID == nil {
		return nil, ErrCapabilityUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, *runtime.MachineID)
	if err != nil {
		return nil, err
	}
	return []guid.ID{agent, machine.PrincipalID}, nil
}

func (m *Module) launchSkillDistillationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in knowledge.DistillationLaunch) (guid.ID, error) {
	if transaction(tx, tenant, in.ProjectID) != nil || in.DistillationID <= 0 || in.WorkItemID <= 0 || in.AgentPrincipalID <= 0 || !commitHash(in.HeadCommit) {
		return 0, ErrInvalidArgument
	}
	parent, err := m.rowOn(ctx, tx, tenant, in.SourceExecutionID, false)
	if err != nil {
		return 0, err
	}
	if parent.ProjectID != in.ProjectID || parent.WorkItemID != in.WorkItemID || parent.State != "SUCCEEDED" {
		return 0, ErrConflict
	}
	source, err := view(parent)
	if err != nil {
		return 0, err
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, in.AgentPrincipalID)
	if err != nil {
		return 0, err
	}
	if !found || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "EXECUTOR") {
		return 0, ErrForbidden
	}
	runtime, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, in.AgentPrincipalID)
	if err != nil {
		return 0, err
	}
	if !found || runtime.MachineID == nil || runtime.RuntimeKind != agent.RuntimeKind {
		return 0, ErrCapabilityUnavailable
	}
	plan := source.Plan
	plan.Distillation, plan.PriceSnapshot, plan.Artifacts, plan.Knowledge, plan.ContextRefs, plan.Toolset = nil, nil, nil, nil, nil, nil
	plan.Runtime, plan.Model, plan.AuthorizationMode = agent.RuntimeKind, agent.ModelID, "READ_ONLY"
	plan.Objective, plan.BaseCommit, plan.QualityFeedback = in.Objective, in.HeadCommit, ""
	created, err := m.createOn(ctx, tx, tenant, ExecutionCreate{ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, MachineID: *runtime.MachineID, Plan: plan, distillation: &in})
	return created.ID, err
}

func (m *Module) skillDistillationOutputOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (knowledge.DistillationOutput, error) {
	var out knowledge.DistillationOutput
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return out, err
	}
	v, err := view(row)
	if err != nil {
		return out, err
	}
	if row.BranchReason != "SKILL_DISTILLATION" || v.Plan.Distillation == nil || row.AssignmentID != 0 {
		return out, ErrForbidden
	}
	out = knowledge.DistillationOutput{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AgentPrincipalID: v.Plan.ExecutorPrincipalID, State: row.State}
	if row.State != "SUCCEEDED" {
		return out, nil
	}
	e := query.BindEvidence(tx, m.names)
	rows, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ExecutionID.Eq(id), e.Kind.Eq("OBSERVATION"), e.Title.Eq("Runtime skill distillation output")).Order(e.ID.Desc()).Limit(2).Find()
	if err != nil {
		return out, err
	}
	// A single model-authored final document is required. Never select tool
	// echoes, generic logs, file metadata, or a caller-supplied replacement.
	if len(rows) == 1 && hash([]byte(rows[0].Content)) == rows[0].ContentHash {
		out.Content = rows[0].Content
	}
	return out, nil
}

// bindToolRelay binds execution-owned validation without exposing storage.
func (m *Module) bindToolRelay(s *module.Services) error {
	connector, e := module.Resolve[agents.ToolRelayExecutionConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if connector.Bind == nil || connector.PinsOn == nil {
		return ErrCapabilityUnavailable
	}
	m.knowledge.toolPins = connector.PinsOn
	return connector.Bind(m.toolRelayExecutionOn)
}
func (m *Module) pinAgentToolsOn(ctx context.Context, tx *gorm.DB, row *model.Execution, p *ExecutionPlan) error {
	p.Toolset = nil
	if p.ExecutorPrincipalID <= 0 {
		return nil
	}
	if m.knowledge.toolPins == nil {
		return nil
	}
	pins, e := m.knowledge.toolPins(executorContext(ctx, row.TenantID, p.ExecutorPrincipalID), tx, row.TenantID, p.ExecutorPrincipalID)
	if e != nil {
		return e
	}
	p.Toolset = pins
	return nil
}
func (m *Module) toolRelayExecutionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (agents.RelayExecution, error) {
	var out agents.RelayExecution
	if e := transaction(tx, tenant, id); e != nil {
		return out, e
	}
	q := query.BindExecutions(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if e != nil {
		return out, agents.ErrForbidden
	}
	if row.State != "RUNNING" || row.CancelRequestedAt != nil || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now().UTC()) || row.DispatchEpoch < 1 {
		return out, agents.ErrForbidden
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(row.PlanJSON))) != row.PlanHash {
		return out, agents.ErrForbidden
	}
	var p ExecutionPlan
	if json.Unmarshal([]byte(row.PlanJSON), &p) != nil || p.ExecutorPrincipalID <= 0 {
		return out, agents.ErrForbidden
	}
	agentCtx := executorContext(ctx, tenant, p.ExecutorPrincipalID)
	if m.work.AuthorizeOn == nil {
		return out, agents.ErrUnavailable
	}
	if e = m.work.AuthorizeOn(agentCtx, tx, link(row, p.ExecutorPrincipalID), "RECEIPT"); e != nil {
		return out, agents.ErrForbidden
	}
	return agents.RelayExecution{AgentID: p.ExecutorPrincipalID, PlanHash: row.PlanHash, DispatchEpoch: row.DispatchEpoch, Tools: p.Toolset}, nil
}

func (m *Module) pinAutomaticKnowledgeOn(ctx context.Context, tx *gorm.DB, row *model.Execution, p *ExecutionPlan) (bool, error) {
	// Automatic retrieval is an Agent command. Human assignments use the
	// ordinary explicit-reference path instead of impersonating Agent zero.
	if p.ExecutorPrincipalID <= 0 || m.knowledge.context.BuildInjectionOn == nil {
		return false, nil
	}
	p.Knowledge = nil
	if m.knowledge.work.ReadOn == nil {
		return true, ErrCapabilityUnavailable
	}
	ids, err := m.knowledge.work.ReadOn(ctx, tx, row.TenantID, row.ProjectID, row.WorkItemID)
	if err != nil {
		return true, err
	}
	for _, ref := range p.ContextRefs {
		if ref.Kind != "KNOWLEDGE" || !slices.Contains(ids, ref.Ref) {
			return true, ErrInvalidArgument
		}
	}
	if len(ids) > 0 {
		if m.knowledge.context.ResolveOn == nil {
			return true, ErrCapabilityUnavailable
		}
		if _, err = m.knowledge.context.ResolveOn(ctx, tx, row.TenantID, row.ProjectID, ids); err != nil {
			return true, err
		}
	}
	in := knowledge.InjectionRequest{TenantID: row.TenantID, ProjectID: row.ProjectID, AgentPrincipalID: p.ExecutorPrincipalID, ExecutionID: row.ID, WorkItemID: row.WorkItemID, Query: knowledge.InjectionQuery{Title: runeExcerpt(p.Objective, 2000), Feedback: []string{runeExcerpt(p.QualityFeedback, 2000)}}, ExplicitIDs: ids}
	result, err := m.knowledge.context.BuildInjectionOn(executorContext(ctx, row.TenantID, p.ExecutorPrincipalID), tx, in)
	if err != nil {
		return true, err
	}
	items := make([]knowledge.ContextItem, 0, len(result.Rules)+len(result.Items))
	for _, item := range append(result.Rules, result.Items...) {
		items = append(items, item.ContextItem)
	}
	if err = m.authorizeKnowledgeSnapshotOn(ctx, tx, row.TenantID, row.ProjectID, items); err != nil {
		return true, err
	}
	p.ContextRefs = nil
	for _, item := range append(result.Rules, result.Items...) {
		p.Knowledge = append(p.Knowledge, item.ContextItem)
		p.ContextRefs = append(p.ContextRefs, ContextRef{Kind: "KNOWLEDGE", Ref: item.EntryID})
	}
	return true, nil
}

// Human execution views retain the exact immutable plan only while the reader
// currently has permission to read every referenced knowledge source.
func (m *Module) authorizeKnowledgeSnapshotOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, items []knowledge.ContextItem) error {
	if len(items) == 0 {
		return nil
	}
	read := m.knowledge.context.AuthorizeSnapshotOn
	if read == nil {
		return ErrCapabilityUnavailable
	}
	err := read(ctx, tx, tenant, project, items)
	if errors.Is(err, knowledge.ErrForbidden) || errors.Is(err, knowledge.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrForbidden
	}
	return err
}

func (m *Module) authorizeKnowledgeViewOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, value ExecutionView) error {
	return m.authorizeKnowledgeSnapshotOn(ctx, tx, tenant, value.ProjectID, value.Plan.Knowledge)
}
func runeExcerpt(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func (m *Module) bindMemory(s *module.Services) error {
	if err := m.bindKnowledgeView(s); err != nil {
		return err
	}
	if err := m.bindToolRelay(s); err != nil {
		return err
	}
	projection, err := module.Resolve[knowledge.RuntimeMetricProjectionConnector](s)
	if err == nil {
		if projection.Bind == nil {
			return ErrCapabilityUnavailable
		}
		if err = projection.Bind(knowledge.RuntimeMetricProjection{ReadOn: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, project *guid.ID, from, to time.Time) ([]knowledge.RuntimeMetricFact, error) {
			source, e := module.Resolve[delivery.MemoryWorkSource](s)
			if e != nil || source.DecisionsOn == nil {
				return nil, knowledge.ErrUnavailable
			}
			decisions, e := source.DecisionsOn(ctx, tx, tenant, project, from, to)
			if e != nil {
				return nil, e
			}
			return m.knowledgeMetricProjectionOn(ctx, tx, tenant, project, from, to, decisions)
		}}); err != nil {
			return err
		}
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	connector, err := module.Resolve[knowledge.MemoryExecutionEvidenceConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrCapabilityUnavailable
	}
	return connector.Bind(knowledge.MemoryExecutionEvidence{VerifyOn: func(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, project *guid.ID, id guid.ID, now time.Time) (string, error) {
		work, err := m.memoryExecutionOn(ctx, tx, tenant, agent, project, id, now)
		if err != nil {
			return "", err
		}
		source, err := module.Resolve[delivery.MemoryWorkSource](s)
		if err != nil || source.ReadOn == nil {
			return "", knowledge.ErrUnavailable
		}
		return source.ReadOn(ctx, tx, tenant, *project, work)
	}})
}

func (m *Module) knowledgeMetricProjectionOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, project *guid.ID, from, to time.Time, decisions []delivery.KnowledgeDecisionFact) ([]knowledge.RuntimeMetricFact, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || !from.Before(to) {
		return nil, knowledge.ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, knowledge.ErrInvalid
	}
	q := query.BindExecutions(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.CreatedAt.Gte(from), q.CreatedAt.Lt(to))
	if project != nil {
		read = read.Where(q.ProjectID.Eq(*project))
	}
	rows, err := read.Order(q.ID).Limit(10001).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, knowledge.ErrUnavailable
	}
	out := []knowledge.RuntimeMetricFact{}
	for _, row := range rows {
		v, e := view(row)
		if e != nil {
			return nil, e
		}
		// pinKnowledgeOn returns without invoking the knowledge provider for an
		// empty reference set. Such plans are not measured retrieval misses.
		attempted := false
		for _, ref := range v.Plan.ContextRefs {
			if ref.Kind == "KNOWLEDGE" {
				attempted = true
				break
			}
		}
		if !attempted {
			continue
		}
		// Explicit references prove assembly, not automatic retrieval or latency.
		out = append(out, knowledge.RuntimeMetricFact{Kind: "INJECTION", ProjectID: row.ProjectID, ExecutionID: row.ID, EvidenceID: row.ID, Hit: len(v.Plan.Knowledge) > 0, OccurredAt: row.CreatedAt})
	}
	for _, decision := range decisions {
		row, e := m.rowOn(ctx, tx, tenant, decision.ExecutionID, false)
		if e != nil {
			return nil, e
		}
		if row.ProjectID != decision.ProjectID || project != nil && row.ProjectID != *project {
			return nil, knowledge.ErrConflict
		}
		v, e := view(row)
		if e != nil {
			return nil, e
		}
		outcome := "ACCEPTED"
		if decision.Kind == "RETURN" {
			outcome = "REJECTED"
		} else if decision.Kind != "ACCEPT" {
			continue
		}
		evidence := query.BindEvidence(tx, m.names)
		facts, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(row.ID)).Order(evidence.ID).Limit(1001).Find()
		if e != nil {
			return nil, e
		}
		if len(facts) > 1000 {
			return nil, knowledge.ErrUnavailable
		}
		for _, item := range v.Plan.Knowledge {
			// The metrics reader also requires its canonical citation receipt.
			cited := false
			for _, fact := range facts {
				if strings.Contains(fact.Content, item.Label) {
					cited = true
					break
				}
			}
			if cited {
				out = append(out, knowledge.RuntimeMetricFact{Kind: "CITATION_OUTCOME", ProjectID: row.ProjectID, ExecutionID: row.ID, EvidenceID: decision.EvidenceID, EntryID: item.EntryID, Outcome: outcome, OccurredAt: decision.CreatedAt})
			}
		}
	}
	return out, nil
}

// memoryExecutionOn verifies server-observed terminal receipts, never a mutable
// updated_at timestamp or a client-supplied source description.
func (m *Module) memoryExecutionOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, project *guid.ID, id guid.ID, now time.Time) (guid.ID, error) {
	if project == nil || *project <= 0 || agent <= 0 || id <= 0 || now.IsZero() {
		return 0, knowledge.ErrMemoryProvenance
	}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if errors.Is(err, ErrNotFound) {
		return 0, knowledge.ErrMemoryProvenance
	}
	if err != nil {
		return 0, err
	}
	if row.ProjectID != *project || (row.State != "SUCCEEDED" && row.State != "FAILED" && row.State != StateCanceled) {
		return 0, knowledge.ErrMemoryProvenance
	}
	v, err := view(row)
	if err != nil {
		return 0, err
	}
	if v.Plan.ExecutorPrincipalID != agent {
		return 0, knowledge.ErrMemoryProvenance
	}
	r := query.BindReceipts(tx, m.names)
	kind := "COMPLETED"
	if row.State == "FAILED" {
		kind = "FAILED"
	}
	if row.State == StateCanceled {
		kind = StateCanceled
	}
	ends, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(id), r.DispatchEpoch.Eq(row.DispatchEpoch), r.Kind.Eq(kind)).Order(r.CreatedAt.Desc(), r.ID.Desc()).Limit(1).Find()
	if err != nil {
		return 0, err
	}
	if len(ends) != 1 || ends[0].CreatedAt.After(now) || ends[0].CreatedAt.Before(now.Add(-24*time.Hour)) {
		return 0, knowledge.ErrMemoryProvenance
	}
	return row.WorkItemID, nil
}

// HubExecutionSession describes real runtime execution and approval facts. The
// current execution model has no INTERACTIVE discriminator; it does not imply
// that a work execution is a separate interactive chat session.
type HubExecutionSession struct {
	ExecutionView
	PendingInteractionCount int  `json:"pending_interaction_count"`
	ViewerOwned             bool `json:"viewer_owned"`
	CurrentResponder        bool `json:"current_responder"`
}
type HubExecutionSessionPage struct {
	Items      []HubExecutionSession `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

func (m *Module) registerHubSessionsHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "execution.sessions.list", Method: http.MethodGet, Path: "/execution-sessions", Summary: "Read own current executions and currently respondable runtime approvals", Tags: []string{"executions"}, Errors: []int{401, 403, 404, 422, 503}}, m.hubSessionsHTTP)
}
func (m *Module) hubSessionsHTTP(ctx context.Context, in *listInput) (*respx.Body[HubExecutionSessionPage], error) {
	project, err := guid.RequireID(in.ProjectID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	cursor := guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, httpError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out HubExecutionSessionPage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.hubSessionsOn(ctx, tx, authn.TenantIDFromContext(ctx), project, cursor, in.Limit)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) hubSessionsOn(ctx context.Context, tx *gorm.DB, tenant, project, after guid.ID, limit int) (HubExecutionSessionPage, error) {
	out := HubExecutionSessionPage{Items: []HubExecutionSession{}}
	if after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	if err := transaction(tx, tenant, project); err != nil {
		return out, err
	}
	if err := m.humanAuthority(ctx, tx, tenant, project, "execution_read"); err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	// Project-level responder permission is necessary but insufficient: each
	// candidate also passes the canonical decision command's work/lease fences.
	responder := true
	if err := m.humanAuthority(ctx, tx, tenant, project, "interaction_respond"); err != nil {
		if !errors.Is(err, ErrForbidden) {
			return out, err
		}
		responder = false
	}
	now := time.Now().UTC()
	q := query.BindExecutions(tx, m.names)
	a := query.BindApprovals(tx, m.names)
	for len(out.Items) <= limit {
		request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Gt(after), q.State.In("RUNNING", "PAUSED"))
		own := q.InitiatorID.Eq(actor)
		if responder {
			pending := a.WithContext(ctx).Select(a.ExecutionID).Where(a.TenantID.Eq(tenant), a.State.Eq("PENDING"), a.ExpiresAt.Gt(now))
			request = request.Where(q.WithContext(ctx).Where(own).Or(q.WithContext(ctx).Columns(q.ID).In(pending)))
		} else {
			request = request.Where(own)
		}
		rows, err := request.Order(q.ID).Limit(100).Find()
		if err != nil {
			return out, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.ID
			count := int64(0)
			canRespond := false
			if responder && row.State == "RUNNING" && row.CancelRequestedAt == nil && row.LeaseExpiresAt != nil && row.LeaseExpiresAt.After(now) {
				if m.work.AuthorizeOn == nil {
					return out, ErrUnavailable
				}
				err = m.work.AuthorizeOn(ctx, tx, link(row, actor), "RECEIPT")
				if err != nil && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
					return out, err
				}
				if err == nil {
					count, err = a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ExecutionID.Eq(row.ID), a.DispatchEpoch.Eq(row.DispatchEpoch), a.State.Eq("PENDING"), a.ExpiresAt.Gt(now)).Count()
					if err != nil {
						return out, err
					}
					canRespond = count > 0
				}
			}
			owned := row.InitiatorID == actor
			if !owned && !canRespond {
				continue
			}
			value, err := view(row)
			if err != nil {
				return out, err
			}
			if err = m.authorizeKnowledgeViewOn(ctx, tx, tenant, value); err != nil {
				return out, err
			}
			out.Items = append(out.Items, HubExecutionSession{ExecutionView: value, ViewerOwned: owned, CurrentResponder: canRespond, PendingInteractionCount: int(count)})
			if len(out.Items) > limit {
				break
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	if len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[len(out.Items)-1].ID.String()
	}
	return out, nil
}

func (m *Module) bindKnowledgeView(s *module.Services) error {
	c, err := module.Resolve[knowledge.ExecutionKnowledgeViewConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.BindExecution(func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (knowledge.ExecutionKnowledgeSnapshot, error) {
		out, e := m.executionKnowledgeViewOn(ctx, tx, tenant, id)
		switch {
		case errors.Is(e, ErrForbidden):
			e = knowledge.ErrForbidden
		case errors.Is(e, ErrNotFound), errors.Is(e, gorm.ErrRecordNotFound):
			e = knowledge.ErrNotFound
		case errors.Is(e, ErrUnavailable), errors.Is(e, ErrCapabilityUnavailable):
			e = knowledge.ErrUnavailable
		case errors.Is(e, ErrConflict):
			e = knowledge.ErrConflict
		}
		return out, e
	})
}
func (m *Module) executionKnowledgeViewOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (knowledge.ExecutionKnowledgeSnapshot, error) {
	out := knowledge.ExecutionKnowledgeSnapshot{}
	row, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return out, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); err != nil {
		return out, err
	}
	v, err := view(row)
	if err != nil {
		return out, err
	}
	if hash([]byte(row.PlanJSON)) != row.PlanHash {
		return out, ErrConflict
	}
	out.ProjectID, out.WorkItemID, out.Budget, out.Items = row.ProjectID, row.WorkItemID, v.Plan.MaxInputTokens, v.Plan.Knowledge
	if row.SourceDecisionID == nil || row.FeedbackSnapshot == "" {
		return out, nil
	}
	var frozen struct {
		Feedback []struct {
			SourceDecisionID guid.ID `json:"source_decision_id"`
			Category         string  `json:"category"`
			Location         string  `json:"location"`
			Expected         string  `json:"expected"`
			Detail           string  `json:"detail"`
		} `json:"feedback"`
	}
	if json.Unmarshal([]byte(row.FeedbackSnapshot), &frozen) != nil {
		return out, nil //nolint:nilerr // Malformed legacy feedback intentionally supplies no context.
	}
	if len(frozen.Feedback) > 100 {
		return out, ErrCapabilityUnavailable
	}
	for i, f := range frozen.Feedback {
		if f.SourceDecisionID != *row.SourceDecisionID {
			return out, ErrConflict
		}
		out.Feedback = append(out.Feedback, knowledge.FeedbackContextItem{Label: fmt.Sprintf("[F%d]", i+1), Category: f.Category, Location: f.Location, Expected: f.Expected, Detail: f.Detail, SourceDecisionID: f.SourceDecisionID})
	}
	evidence := query.BindEvidence(tx, m.names)
	facts, err := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(id)).Order(evidence.ID).Limit(1001).Find()
	if err != nil {
		return out, err
	}
	if len(facts) > 1000 {
		return out, ErrCapabilityUnavailable
	}
	for _, fact := range facts {
		if (fact.Kind != "OBSERVATION" && fact.Kind != "TEST_REPORT") || hash([]byte(fact.Content)) != fact.ContentHash {
			continue
		}
		for i := range out.Feedback {
			if strings.Contains(fact.Content, out.Feedback[i].Label) {
				out.Feedback[i].Cited = true
			}
		}
	}
	return out, nil
}
