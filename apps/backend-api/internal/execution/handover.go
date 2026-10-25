package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type HandoverPlan struct {
	RetirementID     guid.ID `json:"retirement_id"`
	AgentPrincipalID guid.ID `json:"agent_principal_id"`
}
type handoverServices struct {
	authority agents.RetirementHandoverConnector
}

func (m *Module) bindHandover(s *module.Services) error {
	c, err := module.Resolve[agents.RetirementHandoverConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil || c.AuthorizeOn == nil {
		return ErrCapabilityUnavailable
	}
	m.handover.authority = c
	work, err := module.Resolve[delivery.RetirementWork](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	return c.Bind(agents.RetirementHandover{ReconcileOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID) (bool, error) {
		return m.reconcileRetirementExecutionsOn(ctx, tx, tenant, agent, retirement, work.InterruptExecutionOn)
	}, PrepareOn: m.prepareHandoverOn, CancelOn: m.cancelHandoverOn, LaunchOn: m.launchHandoverOn, ReadOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID) (agents.HandoverArtifact, error) {
		v, err := m.readHandoverOn(ctx, tx, tenant, agent, retirement)
		if errors.Is(err, ErrNotFound) {
			err = agents.ErrNotFound
		}
		return v, err
	}, SystemOn: func(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID, content string) (agents.HandoverArtifact, error) {
		if c.AuthorizeArtifactOn == nil {
			return agents.HandoverArtifact{}, ErrCapabilityUnavailable
		}
		if err := c.AuthorizeArtifactOn(ctx, tx, tenant, agent, retirement); err != nil {
			return agents.HandoverArtifact{}, err
		}
		return m.saveHandoverOn(ctx, tx, tenant, agent, retirement, nil, content)
	}})
}

func handoverArtifactView(r *model.HandoverArtifact) agents.HandoverArtifact {
	return agents.HandoverArtifact{ID: r.ID, ExecutionID: r.ExecutionID, Source: r.Source, Content: r.Content, SHA256: r.SHA256}
}

func (m *Module) saveHandoverOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID, execution *guid.ID, content string) (agents.HandoverArtifact, error) {
	if transaction(tx, tenant, retirement) != nil || agent <= 0 || !utf8.ValidString(content) || strings.TrimSpace(content) == "" || len(content) > 65536 {
		return agents.HandoverArtifact{}, ErrInvalidArgument
	}
	source := "SYSTEM"
	if execution != nil {
		if *execution <= 0 {
			return agents.HandoverArtifact{}, ErrInvalidArgument
		}
		source = "AGENT"
	}
	id, err := m.nextID()
	if err != nil {
		return agents.HandoverArtifact{}, err
	}
	r := model.HandoverArtifact{ID: id, TenantID: tenant, RetirementID: retirement, AgentPrincipalID: agent, ExecutionID: execution, Source: source, Content: content, SHA256: hash([]byte(content)), CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = query.BindHandoverArtifacts(tx, m.names).WithContext(ctx).Create(&r); err != nil {
		return agents.HandoverArtifact{}, err
	}
	return handoverArtifactView(&r), nil
}

func (m *Module) readHandoverOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID) (agents.HandoverArtifact, error) {
	if m.handover.authority.AuthorizeOn == nil {
		return agents.HandoverArtifact{}, ErrCapabilityUnavailable
	}
	authorize := m.handover.authority.AuthorizeArtifactOn
	if authorize == nil {
		authorize = m.handover.authority.AuthorizeOn
	}
	if err := authorize(ctx, tx, tenant, agent, retirement); err != nil {
		return agents.HandoverArtifact{}, err
	}
	q := query.BindHandoverArtifacts(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.RetirementID.Eq(retirement), q.AgentPrincipalID.Eq(agent)).Order(q.ID.Desc()).Limit(4).Find()
	if err != nil {
		return agents.HandoverArtifact{}, err
	}
	for _, r := range rows {
		if hash([]byte(r.Content)) != r.SHA256 {
			return agents.HandoverArtifact{}, ErrUnavailable
		}
		if r.ExecutionID != nil {
			e, err := m.rowOn(ctx, tx, tenant, *r.ExecutionID, false)
			if err != nil {
				return agents.HandoverArtifact{}, err
			}
			v, err := view(e)
			if err != nil {
				return agents.HandoverArtifact{}, err
			}
			if v.Plan.Handover == nil || v.Plan.Handover.RetirementID != retirement || v.Plan.Handover.AgentPrincipalID != agent {
				return agents.HandoverArtifact{}, ErrConflict
			}
			if e.State != "SUCCEEDED" {
				continue
			}
		}
		return handoverArtifactView(r), nil
	}
	return agents.HandoverArtifact{}, ErrNotFound
}

// Prepare precedes protocol/business row locks. The runtime binding is checked
// again by Launch, so a concurrent rebind cannot lend another machine's identity.
func (m *Module) prepareHandoverOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, additional ...guid.ID) error {
	if m.agentRuntime.ByPrincipalOn == nil || m.machines.ByIDOn == nil {
		return ErrCapabilityUnavailable
	}
	readRuntime := m.agentRuntime.RetirementOn
	if readRuntime == nil {
		readRuntime = m.agentRuntime.ByPrincipalOn
	}
	r, found, err := readRuntime(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	if !found {
		return ErrConflict
	}
	ids := append([]guid.ID{authn.PrincipalIDFromContext(ctx), agent}, additional...)
	executions, err := m.retirementExecutionRowsOn(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.BranchReason == "HANDOVER" {
			continue
		}
		principals, err := m.dispatchStopPrincipalsOn(ctx, tx, tenant, execution.ProjectID, execution.WorkItemID, execution.ID)
		if err != nil {
			return err
		}
		ids = append(ids, principals...)
	}
	if r.MachineID == nil {
		return m.principalLocks(ctx, tx, ids...)
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, *r.MachineID)
	if err != nil {
		return err
	}
	return m.principalLocks(ctx, tx, append(ids, machine.PrincipalID)...)
}

// Stop the internal producer with explicit UNKNOWN runtime outcome when its
// lease was delivered. Incrementing the epoch fences any late completion; this
// never asserts that a process stopped or that an Agent document was produced.
func (m *Module) cancelHandoverOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement, execution guid.ID) error {
	authorize := m.handover.authority.AuthorizeArtifactOn
	if authorize == nil {
		authorize = m.handover.authority.AuthorizeOn
	}
	if authorize == nil {
		return ErrCapabilityUnavailable
	}
	if err := authorize(ctx, tx, tenant, agent, retirement); err != nil {
		return err
	}
	q := query.BindExecutions(tx, m.names)
	if execution <= 0 {
		return ErrInvalidArgument
	}
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(execution), q.BranchReason.Eq("HANDOVER")).Find()
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return ErrNotFound
	}
	for _, snapshot := range rows {
		v, err := view(snapshot)
		if err != nil {
			return err
		}
		if v.Plan.Handover == nil || v.Plan.Handover.RetirementID != retirement || v.Plan.Handover.AgentPrincipalID != agent {
			return ErrForbidden
		}
		if err = m.lockMachineOn(ctx, tx, tenant, snapshot.MachineID); err != nil {
			return err
		}
		r, err := m.rowOn(ctx, tx, tenant, snapshot.ID, true)
		if err != nil {
			return err
		}
		if !active(r.State) {
			continue
		}
		if r.DispatchEpoch == int64(^uint64(0)>>1) {
			return ErrConflict
		}
		if m.machines.ReleaseSlotOn == nil {
			return ErrCapabilityUnavailable
		}
		if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, r.MachineID, r.ID); err != nil {
			return err
		}
		result := "UNKNOWN"
		if r.State == "CREATED" && r.LeaseExpiresAt == nil {
			result = "CONFIRMED"
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID), q.Version.Eq(r.Version)).Updates(map[string]any{"state": StateCanceled, "state_reason": "RETIREMENT_HANDOVER_ENDED", "result_state": result, "cancel_requested_at": now, "lease_expires_at": nil, "dispatch_epoch": r.DispatchEpoch + 1, "version": r.Version + 1, "updated_at": now})
		if err != nil {
			return err
		}
		if changed.RowsAffected != 1 {
			return ErrConflict
		}
		r.State, r.ResultState, r.Version = StateCanceled, result, r.Version+1
		if err = m.eventOn(ctx, tx, r, "execution.state_changed", "HUMAN", "IAM:agent_retire", map[string]any{"state": r.State, "purpose": "HANDOVER", "outcome": result}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) launchHandoverOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID, objective string) (guid.ID, error) {
	if transaction(tx, tenant, retirement) != nil || agent <= 0 || !utf8.ValidString(objective) || strings.TrimSpace(objective) == "" || len(objective) > 20000 {
		return 0, ErrInvalidArgument
	}
	if m.handover.authority.AuthorizeOn == nil || m.agents.ByPrincipalOn == nil || m.agentRuntime.ByPrincipalOn == nil || m.machines.ReserveSlotOn == nil || m.machines.ByIDOn == nil {
		return 0, ErrCapabilityUnavailable
	}
	seed, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return 0, err
	}
	if !found || seed.MachineID == nil {
		return 0, ErrConflict
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, *seed.MachineID)
	if err != nil {
		return 0, err
	}
	if err := m.principalLocks(ctx, tx, authn.PrincipalIDFromContext(ctx), agent, machine.PrincipalID); err != nil {
		return 0, err
	}
	if err := m.handover.authority.AuthorizeOn(ctx, tx, tenant, agent, retirement); err != nil {
		return 0, err
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return 0, err
	}
	if !found || a.State != "RETIRING" || a.TenantID != tenant || a.PrincipalID != agent || (a.RuntimeKind != "CODEX" && a.RuntimeKind != "CLAUDE_CODE") || a.ModelID == "" {
		return 0, ErrForbidden
	}
	runtime, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return 0, err
	}
	if !found || runtime.MachineID == nil || *runtime.MachineID != *seed.MachineID || runtime.MachineState != "ONLINE" || runtime.RuntimeKind != a.RuntimeKind {
		return 0, ErrConflict
	}
	if !utf8.ValidString(a.SystemPrompt) || len(a.SystemPrompt) > 20000 {
		return 0, ErrInvalidArgument
	}
	id, err := m.nextID()
	if err != nil {
		return 0, err
	}
	p := ExecutionPlan{Handover: &HandoverPlan{RetirementID: retirement, AgentPrincipalID: agent}, Runtime: a.RuntimeKind, Model: a.ModelID, ModelFamily: a.ModelFamily, ExecutorPrincipalID: agent, AgentSystemPrompt: a.SystemPrompt, AuthorizationMode: "READ_ONLY", MaxInputTokens: 32000, MaxOutputTokens: 8000, Objective: objective}
	p.Correlation, err = newCorrelation(ctx, nil)
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := model.Execution{ID: id, TenantID: tenant, MachineID: *runtime.MachineID, InitiatorID: authn.PrincipalIDFromContext(ctx), BranchReason: "HANDOVER", State: "CREATED", StateReason: "HANDOVER", ResultState: "NONE", PlanJSON: string(encoded), PlanHash: hash(encoded), DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, err = m.machines.ReserveSlotOn(ctx, tx, tenant, r.MachineID, id); err != nil {
		return 0, err
	}
	if err = query.BindExecutions(tx, m.names).WithContext(ctx).Create(&r); err != nil {
		return 0, err
	}
	if err = m.eventOn(ctx, tx, &r, "execution.state_changed", "HUMAN", "IAM:agent_retire", map[string]any{"state": r.State, "purpose": "HANDOVER", "retirement_id": retirement.String()}); err != nil {
		return 0, err
	}
	return id, nil
}

// Recheck both the real initiator's current retirement authority and the
// retiring identity. Machine authentication remains the existing Runner fence.
func (m *Module) authorizeHandoverOn(ctx context.Context, tx *gorm.DB, row *model.Execution, historical bool) (ExecutionView, error) {
	v, err := view(row)
	if err != nil {
		return v, err
	}
	if row.BranchReason != "HANDOVER" || v.Plan.Handover == nil || row.WorkItemID != 0 || row.AssignmentID != 0 || row.InitiatorID <= 0 {
		return v, ErrForbidden
	}
	p := v.Plan.Handover
	if m.handover.authority.AuthorizeOn == nil || m.agents.ByPrincipalOn == nil || m.agentRuntime.ByPrincipalOn == nil {
		return v, ErrCapabilityUnavailable
	}
	if err = m.principalLocks(ctx, tx, authn.PrincipalIDFromContext(ctx), row.InitiatorID, p.AgentPrincipalID); err != nil {
		return v, err
	}
	human := authn.WithClaims(ctx, &authn.Claims{TenantID: row.TenantID, PrincipalID: row.InitiatorID, SubjectType: authn.SubjectTypePrincipal})
	authorize := m.handover.authority.AuthorizeOn
	if historical && m.handover.authority.AuthorizeArtifactOn != nil {
		authorize = m.handover.authority.AuthorizeArtifactOn
	}
	if err = authorize(human, tx, row.TenantID, p.AgentPrincipalID, p.RetirementID); err != nil {
		return v, err
	}
	if historical {
		return v, nil
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, row.TenantID, p.AgentPrincipalID)
	if err != nil {
		return v, err
	}
	if !found || a.State != "RETIRING" || a.RuntimeKind != v.Plan.Runtime || a.ModelID != v.Plan.Model || a.ModelFamily != v.Plan.ModelFamily {
		return v, ErrForbidden
	}
	runtime, found, err := m.agentRuntime.ByPrincipalOn(ctx, tx, row.TenantID, p.AgentPrincipalID)
	if err != nil {
		return v, err
	}
	if !found || runtime.MachineID == nil || *runtime.MachineID != row.MachineID || runtime.RuntimeKind != v.Plan.Runtime {
		return v, ErrForbidden
	}
	return v, nil
}

func (m *Module) pullHandoverOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) ([]ExecutionView, bool, error) {
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(f.MachineID), q.BranchReason.Eq("HANDOVER"), q.State.Eq("CREATED"), q.LeaseExpiresAt.IsNull()).Order(q.ID).Limit(1).Find()
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	row := rows[0]
	v, err := m.authorizeHandoverOn(ctx, tx, row, false)
	if err != nil {
		return nil, true, err
	}
	if err = m.validateRunner(ctx, tx, tenant, f); err != nil {
		return nil, true, err
	}
	row, err = m.rowOn(ctx, tx, tenant, row.ID, true)
	if err != nil {
		return nil, true, err
	}
	if row.State != "CREATED" || row.LeaseExpiresAt != nil {
		return []ExecutionView{}, true, nil
	}
	expires := time.Now().UTC().Truncate(time.Millisecond).Add(30 * time.Second)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"lease_expires_at": expires, "version": row.Version + 1})
	if err != nil {
		return nil, true, err
	}
	if changed.RowsAffected != 1 {
		return nil, true, ErrConflict
	}
	row.Version++
	row.LeaseExpiresAt = &expires
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"state": row.State, "leased": true, "purpose": "HANDOVER"}); err != nil {
		return nil, true, err
	}
	v.Version = row.Version
	v.LeaseExpiresAt = &expires
	return []ExecutionView{v}, true, nil
}

// Handover receipts deliberately do not enter Delivery's work/Claim commands or
// repository/file-artifact pipeline. The immutable document is the sole output.
func (m *Module) receiptHandoverOn(ctx context.Context, tx *gorm.DB, snapshot *model.Execution, f RunnerFence, in ReceiptInput) (ReceiptView, error) {
	var empty ReceiptView
	if in.Upload != nil || in.Usage != nil || in.HeadCommit != "" || in.EvidenceKind != "" || in.EvidenceTitle != "" {
		return empty, ErrInvalidArgument
	}
	if _, err := m.authorizeHandoverOn(ctx, tx, snapshot, true); err != nil {
		return empty, err
	}
	if err := m.validateRunner(ctx, tx, snapshot.TenantID, f); err != nil {
		return empty, err
	}
	row, err := m.rowOn(ctx, tx, snapshot.TenantID, snapshot.ID, true)
	if err != nil {
		return empty, err
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return empty, err
	}
	digest := hash(encoded)
	if row.MachineID != f.MachineID || row.DispatchEpoch != in.DispatchEpoch {
		return empty, ErrFenced
	}
	r := query.BindReceipts(tx, m.names)
	prior, err := r.WithContext(ctx).Where(r.TenantID.Eq(row.TenantID), r.ExecutionID.Eq(row.ID), r.DispatchEpoch.Eq(in.DispatchEpoch), r.Seq.Eq(in.Seq)).First()
	if err == nil {
		if prior.ContentHash != digest {
			return empty, ErrConflict
		}
		return ReceiptView{Seq: in.Seq, State: prior.State, Replayed: true, CancelRequested: row.CancelRequestedAt != nil}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	// A committed receipt can replay after retirement advances or completes.
	// Only a new receipt needs the active HANDOVER/runtime authority again.
	if _, err := m.authorizeHandoverOn(ctx, tx, row, false); err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(now) || row.LastSeq == int64(^uint64(0)>>1) || in.Seq != row.LastSeq+1 {
		return empty, ErrFenced
	}
	next, result, reason := row.State, row.ResultState, row.StateReason
	switch in.Kind {
	case "ACCEPTED":
		if row.State != "CREATED" || row.CancelRequestedAt != nil {
			return empty, ErrConflict
		}
		next = "RUNNING"
	case "HEARTBEAT", "LOG":
		if row.State != "RUNNING" {
			return empty, ErrConflict
		}
		if in.Kind == "LOG" {
			if in.Content == "" {
				return empty, ErrInvalidArgument
			}
			id, err := m.nextID()
			if err != nil {
				return empty, err
			}
			if err = query.BindLogs(tx, m.names).WithContext(ctx).Create(&model.Log{ID: id, TenantID: row.TenantID, ExecutionID: row.ID, DispatchEpoch: row.DispatchEpoch, Seq: in.Seq, Content: in.Content, ContentHash: hash([]byte(in.Content)), CreatedAt: now}); err != nil {
				return empty, err
			}
		}
	case "COMPLETED":
		if row.State != "RUNNING" || row.CancelRequestedAt != nil {
			return empty, ErrConflict
		}
		v, err := view(row)
		if err != nil {
			return empty, err
		}
		if _, err = m.saveHandoverOn(ctx, tx, row.TenantID, v.Plan.Handover.AgentPrincipalID, v.Plan.Handover.RetirementID, &row.ID, in.Content); err != nil {
			return empty, err
		}
		next, result, reason = "SUCCEEDED", "CONFIRMED", "HANDOVER_COMPLETED"
	case "FAILED":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, result, reason = "FAILED", "CONFIRMED", "RUNTIME_ERROR"
	case "LOST":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, result, reason = "INTERRUPTED", "UNKNOWN", "HEARTBEAT_LOST"
	case StateCanceled:
		if !active(row.State) || row.CancelRequestedAt == nil {
			return empty, ErrConflict
		}
		next, result, reason = StateCanceled, "CONFIRMED", "USER_CANCEL"
	default:
		return empty, ErrInvalidArgument
	}
	terminal := !active(next)
	expiry := now.Add(30 * time.Second)
	var lease *time.Time = &expiry
	if terminal {
		lease = nil
		if m.machines.ReleaseSlotOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if err = m.machines.ReleaseSlotOn(ctx, tx, row.TenantID, row.MachineID, row.ID); err != nil {
			return empty, err
		}
	}
	q := query.BindExecutions(tx, m.names)
	updated, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": next, "state_reason": reason, "result_state": result, "last_seq": in.Seq, "lease_expires_at": lease, "version": row.Version + 1, "updated_at": now})
	if err != nil {
		return empty, err
	}
	if updated.RowsAffected != 1 {
		return empty, ErrConflict
	}
	id, err := m.nextID()
	if err != nil {
		return empty, err
	}
	if err = r.WithContext(ctx).Create(&model.Receipt{ID: id, TenantID: row.TenantID, ExecutionID: row.ID, DispatchEpoch: row.DispatchEpoch, Seq: in.Seq, Kind: in.Kind, ContentHash: digest, State: next, CreatedAt: now}); err != nil {
		return empty, err
	}
	row.State, row.StateReason, row.ResultState, row.Version = next, reason, result, row.Version+1
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"state": next, "purpose": "HANDOVER", "receipt_kind": in.Kind, "seq": in.Seq}); err != nil {
		return empty, err
	}
	return ReceiptView{Seq: in.Seq, State: next, CancelRequested: row.CancelRequestedAt != nil}, nil
}
