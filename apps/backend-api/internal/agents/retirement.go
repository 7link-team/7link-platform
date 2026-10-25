package agents

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RetirementStart struct {
	Mode        string `json:"mode" enum:"NORMAL,FORCED"`
	Reason      string `json:"reason" minLength:"10" maxLength:"2000"`
	ConfirmName string `json:"confirm_name,omitempty" maxLength:"200"`
	ReauthProof string `json:"reauth_proof,omitempty" maxLength:"512"`
}

type RetirementExecution = model.RetirementExecution

var ErrRetirementNameMismatch = errors.New("agents: retirement confirmation name mismatch")

// Execution binds its authoritative reader after Agents is wired; no Execution
// storage or runtime identity is owned by the retirement coordinator.
type RetirementExecutionConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error)) error
}

func (m *Module) provideRetirement(s *module.Services) error {
	if err := m.provideRetirementHandover(s); err != nil {
		return err
	}
	return module.Provide(s, RetirementExecutionConnector{Bind: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error)) error {
		if read == nil || m.retirementExecutions != nil {
			return ErrConflict
		}
		m.retirementExecutions = read
		return nil
	}})
}

// The runtime provider owns locking and the current Fleet version. The protocol
// stores that returned version, never a version inferred from a previous read.
func (m *Module) injectRetirement(s *module.Services) error {
	rules, err := module.Resolve[knowledge.RetirementRules](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.retirementRules = rules
	v, err := module.Resolve[fleet.AgentRetirementRuntime](s)
	if errors.Is(err, module.ErrServiceMissing) {
		slog.Warn("Agent retirement unavailable: Fleet retirement runtime capability missing")
		return nil
	}
	if err != nil {
		return err
	}
	m.retirementRuntime = v
	work, err := module.Resolve[delivery.RetirementWork](s)
	if errors.Is(err, module.ErrServiceMissing) {
		slog.Warn("Agent retirement precheck unavailable: Delivery retirement work capability missing")
		return nil
	}
	if err != nil {
		return err
	}
	m.retirementWork = work
	return nil
}

func (m *Module) startRetirementOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, agentVersion int, in RetirementStart) (model.Retirement, error) {
	var empty model.Retirement
	if agentVersion < 1 || (in.Mode != "NORMAL" && in.Mode != "FORCED") || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return empty, ErrInvalid
	}
	_, err := m.authorize(ctx, tx, tenant, agent, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	if in.Mode == "FORCED" {
		if in.ReauthProof == "" || m.forcedRetirementProof == nil {
			return empty, ErrForbidden
		}
		if m.retirementRuntime.ForceOn == nil {
			return empty, ErrUnavailable
		}
		if err = m.forcedRetirementProof(ctx, tx, tenant, agent, in.ReauthProof); err != nil {
			return empty, ErrForbidden
		}
	}
	if (in.Mode == "NORMAL" && m.retirementRuntime.BeginOn == nil) || m.ids == nil || m.audit == nil || m.events == nil {
		return empty, ErrUnavailable
	}
	// Serialize the active-record check with the canonical principal lock. Fleet
	// repeats runtime state and version checks under its Agent row lock below.
	if m.principals.LockActiveRow == nil {
		return empty, ErrUnavailable
	}
	if _, active, err := m.principals.LockActiveRow(ctx, tx, agent); err != nil {
		return empty, err
	} else if !active {
		return empty, ErrConflict
	}
	q := query.BindRetirement(tx, m.names)
	active, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ActiveAgentID.Eq(agent)).Limit(2).Find()
	if err != nil {
		return empty, err
	}
	if len(active) > 1 || (len(active) != 0 && in.Mode != "FORCED") {
		return empty, ErrConflict
	}
	if m.retirementWork.ListOn == nil {
		return empty, ErrUnavailable
	}
	assignments, err := m.retirementWork.ListOn(ctx, tx, tenant, agent)
	if err != nil {
		return empty, retirementWorkError(err)
	}
	if m.retirementExecutions == nil {
		return empty, ErrUnavailable
	}
	executions, err := m.retirementExecutions(ctx, tx, tenant, agent)
	if err != nil {
		return empty, err
	}
	precheck := model.RetirementPrecheck{WorkItems: make([]model.RetirementWorkItem, 0, len(assignments))}
	precheck.Executions = executions
	if m.retirementWork.LeadProjectsOn != nil {
		precheck.LeadProjectIDs, err = m.retirementWork.LeadProjectsOn(ctx, tx, tenant, agent)
		if err != nil {
			return empty, retirementWorkError(err)
		}
	}
	if m.runtimeFacts.RetirementOn != nil {
		fact, found, err := m.runtimeFacts.RetirementOn(ctx, tx, tenant, agent)
		if err != nil {
			return empty, err
		}
		if found {
			precheck.Machine = &model.RetirementMachine{MachineID: fact.MachineID, MachineState: fact.MachineState, ProcessStatus: fact.ProcessStatus}
		}
	}
	for _, item := range assignments {
		precheck.WorkItems = append(precheck.WorkItems, model.RetirementWorkItem{AssignmentID: item.AssignmentID, ProjectID: item.ProjectID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, AssignmentVersion: item.AssignmentVersion, Title: item.Title, State: item.State, BlockReasons: item.BlockReasons, BlockNote: item.BlockNote})
	}
	var id guid.ID
	if len(active) == 1 {
		id = active[0].ID
	} else {
		rawID, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		id = guid.ID(rawID)
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	var binding fleet.AgentRetirementBinding
	var forcedSummary string
	if in.Mode == "FORCED" {
		// Freeze readable sources before Fleet freezes private rule memories.
		// Missing summary data must not prevent credential revocation.
		_ = tx.Transaction(func(summaryTx *gorm.DB) error {
			var err error
			snapshot := model.Retirement{ID: id, TenantID: tenant, AgentPrincipalID: agent, Precheck: precheck}
			forcedSummary, err = m.retirementSummaryOn(ctx, summaryTx, &snapshot)
			precheck = snapshot.Precheck
			return err
		})
	}
	wantState := "RETIRING"
	if in.Mode == "FORCED" {
		precheck.Lost = &model.RetirementLoss{OpenItems: len(assignments), RunningExecutions: len(executions)}
		binding, err = m.retirementRuntime.ForceOn(ctx, tx, tenant, agent, id, agentVersion, in.ConfirmName, fleet.RetirementLoss{OpenItems: len(assignments), RunningExecutions: len(executions)})
		wantState = "RETIRED"
	} else {
		binding, err = m.retirementRuntime.BeginOn(ctx, tx, tenant, agent, id, agentVersion)
	}
	if err != nil {
		return empty, retirementRuntimeError(err)
	}
	if binding.PrincipalID != agent || binding.RetirementID != id || binding.State != wantState || binding.Version <= agentVersion {
		return empty, ErrUnavailable
	}
	now := m.now()
	r := model.Retirement{ID: id, TenantID: tenant, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "HANDOVER", Reason: in.Reason, Precheck: precheck, AgentVersion: binding.Version, Version: 1, CreatedBy: actorID(ctx), CreatedAt: now, StepStartedAt: now}
	if in.Mode == "FORCED" {
		r.Mode, r.State, r.Step, r.ActiveAgentID, r.CompletedAt = "FORCED", "COMPLETED", "DONE", nil, &now
	}
	if len(active) == 1 {
		r.Version = active[0].Version + 1
		r.CreatedBy, r.CreatedAt = active[0].CreatedBy, active[0].CreatedAt
		changed, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.Version.Eq(active[0].Version)).Select(q.Mode, q.State, q.Step, q.Reason, q.Precheck, q.AgentVersion, q.Version, q.CreatedBy, q.CreatedAt, q.StepStartedAt, q.CompletedAt, q.ActiveAgentID).Updates(&r)
		if e != nil {
			return empty, e
		}
		if changed.RowsAffected != 1 {
			return empty, ErrConflict
		}
	} else {
		if err = q.WithContext(ctx).Create(&r); err != nil {
			return empty, err
		}
	}
	if in.Mode == "FORCED" {
		if err = m.saveForcedRetirementSummaryOn(ctx, tx, &r, forcedSummary); err != nil {
			return empty, err
		}
	}
	if err = m.recordRetirementOn(ctx, tx, &r, "START"); err != nil {
		return empty, err
	}
	return r, nil
}

func retirementWorkError(err error) error {
	switch {
	case errors.Is(err, delivery.ErrWorkForbidden), errors.Is(err, delivery.ErrProjectForbidden):
		return ErrForbidden
	case errors.Is(err, delivery.ErrWorkVersion), errors.Is(err, delivery.ErrAssignmentState):
		return ErrConflict
	case errors.Is(err, delivery.ErrUnavailable):
		return ErrUnavailable
	default:
		return err
	}
}

func (m *Module) retirementOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string, lock bool) (*model.Retirement, error) {
	if tenant <= 0 || id <= 0 || tx == nil {
		return nil, ErrInvalid
	}
	q := query.BindRetirement(tx, m.names)
	// Find the target without locking; authorize locks the acting principal
	// before the retirement row. Re-read and lock the record after authority.
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if permission == "agent_retire" && r.State == "IN_PROGRESS" && (r.Step == "HANDOVER" || r.Step == "SUCCESSOR") && r.Precheck.Progress != nil && r.Precheck.Progress.HandoverExecutionID != nil && m.retirementHandover.PrepareOn != nil {
		if err = m.retirementHandover.PrepareOn(ctx, tx, tenant, r.AgentPrincipalID); err != nil {
			return nil, err
		}
	}
	if _, err = m.authorize(ctx, tx, tenant, r.AgentPrincipalID, permission, true); err != nil {
		return nil, err
	}
	if lock {
		if _, active, e := m.principals.LockActiveRow(ctx, tx, r.AgentPrincipalID); e != nil {
			return nil, e
		} else if !active {
			return nil, ErrConflict
		}
		return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	}
	return r, nil
}

func (m *Module) cancelRetirementOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version, agentVersion int) (model.Retirement, error) {
	var empty model.Retirement
	r, err := m.retirementOn(ctx, tx, tenant, id, "agent_retire", true)
	if err != nil {
		return empty, err
	}
	if version < 1 || r.Version != version || agentVersion < 1 || r.State != "IN_PROGRESS" || (r.Step != "PRECHECK" && r.Step != "HANDOVER" && r.Step != "SUCCESSOR") {
		return empty, ErrConflict
	}
	if m.retirementRuntime.CancelOn == nil {
		return empty, ErrUnavailable
	}
	if r.Step == "HANDOVER" && r.Precheck.Progress != nil && r.Precheck.Progress.HandoverExecutionID != nil {
		if m.retirementHandover.CancelOn == nil {
			return empty, ErrUnavailable
		}
		if err = m.retirementHandover.CancelOn(ctx, tx, tenant, r.AgentPrincipalID, r.ID, *r.Precheck.Progress.HandoverExecutionID); err != nil {
			return empty, err
		}
	}
	// A Runner ACK can legitimately advance Fleet's version independently of
	// this record. Require the caller's current Fleet fence, not a cached one.
	binding, err := m.retirementRuntime.CancelOn(ctx, tx, tenant, r.AgentPrincipalID, id, agentVersion)
	if err != nil {
		return empty, retirementRuntimeError(err)
	}
	if binding.PrincipalID != r.AgentPrincipalID || binding.RetirementID != id || binding.State != "STOPPED" || binding.Version <= agentVersion {
		return empty, ErrUnavailable
	}
	now := m.now()
	q := query.BindRetirement(tx, m.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version), q.State.Eq("IN_PROGRESS")).Updates(map[string]any{"state": "CANCELLED", "active_agent_id": nil, "completed_at": now, "agent_version": binding.Version, "version": version + 1}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	if err != nil {
		return empty, err
	}
	if changed.RowsAffected != 1 {
		return empty, ErrConflict
	}
	r.State, r.ActiveAgentID, r.CompletedAt, r.AgentVersion, r.Version = "CANCELLED", nil, &now, binding.Version, version+1 //nolint:misspell // Preserve the published domain or serialized contract spelling.
	if err = m.recordRetirementOn(ctx, tx, r, "CANCEL"); err != nil {
		return empty, err
	}
	return *r, nil
}

func retirementRuntimeError(err error) error {
	switch {
	case errors.Is(err, fleet.ErrRetirementNameMismatch):
		return ErrRetirementNameMismatch
	case errors.Is(err, fleet.ErrVersionConflict), errors.Is(err, fleet.ErrAgentRebindState), errors.Is(err, fleet.ErrAgentRunningExecutions):
		return ErrConflict
	case errors.Is(err, fleet.ErrMachineForbidden):
		return ErrForbidden
	case errors.Is(err, fleet.ErrMachineOffline):
		return ErrConflict
	default:
		return profileError(err)
	}
}

func (m *Module) recordRetirementOn(ctx context.Context, tx *gorm.DB, r *model.Retirement, action string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, r.TenantID, "agents.changed", "agent_retirement", r.ID)
	e.Detail["authority_source"], e.Detail["action"], e.Detail["reason"] = "IAM:agent_retire", action, r.Reason
	e.Detail["retirement_id"], e.Detail["agent_principal_id"], e.Detail["state"], e.Detail["step"] = r.ID, r.AgentPrincipalID, r.State, r.Step
	if r.Mode == "FORCED" {
		e.Detail["lost"] = r.Precheck.Lost
		e.Detail["handover_source"] = nil
		if p := r.Precheck.Progress; p != nil {
			if p.HandoverSource != "" {
				e.Detail["handover_source"] = p.HandoverSource
				e.Detail["handover_artifact_id"] = p.HandoverArtifactID
			}
			if p.HandoverErrorCode != "" {
				e.Detail["handover_error_code"] = p.HandoverErrorCode
			}
		}
	}
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: "agents.changed", Actor: events.Actor{Kind: "HUMAN", ID: actorID(ctx), AuthoritySource: "IAM:agent_retire"}, Subject: events.Subject{Kind: "agent", ID: r.AgentPrincipalID}, SubjectVersion: int64(r.Version), Data: map[string]any{"retirement_id": r.ID, "action": action, "agent_principal_id": r.AgentPrincipalID, "state": r.State, "step": r.Step}})
	return err
}
