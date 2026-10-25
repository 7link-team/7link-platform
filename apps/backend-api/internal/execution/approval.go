package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const approvalProtocol = "CLAUDE_CONTROL_V1"

type ApprovalRequest struct {
	DispatchEpoch int64           `json:"dispatch_epoch" minimum:"1"`
	RequestID     string          `json:"request_id" minLength:"1" maxLength:"160"`
	Tool          string          `json:"tool" minLength:"1" maxLength:"160"`
	Input         json.RawMessage `json:"input"`
}
type ApprovalView struct {
	ID          guid.ID         `json:"id"`
	ExecutionID guid.ID         `json:"execution_id"`
	RequestID   string          `json:"request_id"`
	Tool        string          `json:"tool"`
	Input       json.RawMessage `json:"input"`
	State       string          `json:"state" enum:"PENDING,ALLOW,DENY,EXPIRED"`
	Reason      string          `json:"reason"`
	ExpiresAt   time.Time       `json:"expires_at"`
	ActorID     *guid.ID        `json:"actor_id,omitempty"`
}

func approvalView(r *model.Approval) ApprovalView {
	state := r.State
	if !r.ExpiresAt.After(time.Now()) {
		state = "EXPIRED"
	}
	return ApprovalView{ID: r.ID, ExecutionID: r.ExecutionID, RequestID: r.RequestID, Tool: r.Tool, Input: json.RawMessage(r.InputJSON), State: state, Reason: r.Reason, ExpiresAt: r.ExpiresAt, ActorID: r.ActorID}
}

// lockApprovalRunner preserves principal/project/machine/execution lock order.
func (m *Module) lockApprovalRunner(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, f RunnerFence, epoch int64) (*model.Execution, error) {
	snap, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	if snap.MachineID != f.MachineID {
		return nil, ErrForbidden
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err = m.workPrincipalLocks(ctx, tx, link(snap, actor), actor, snap.InitiatorID); err != nil {
		return nil, err
	}
	if m.work.AuthorizeOn == nil {
		return nil, ErrUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(snap, actor), "RECEIPT"); err != nil {
		return nil, err
	}
	if err = m.validateRunner(ctx, tx, tenant, f); err != nil {
		return nil, err
	}
	row, err := m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return nil, err
	}
	if row.State != "RUNNING" || row.CancelRequestedAt != nil || row.DispatchEpoch != epoch || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) {
		return nil, ErrFenced
	}
	v, err := view(row)
	if err != nil {
		return nil, err
	}
	if v.Plan.AuthorizationMode != "ON_REQUEST" || !validPlanApprovalProtocol(v.Plan) {
		return nil, ErrApprovalUnavailable
	}
	return row, nil
}

func (m *Module) RequestApprovalOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, f RunnerFence, in ApprovalRequest) (ApprovalView, error) {
	if !bounded(in.RequestID, 160) || !bounded(in.Tool, 160) || in.DispatchEpoch <= 0 || len(in.Input) > 16384 || !json.Valid(in.Input) || len(in.Input) == 0 || in.Input[0] != '{' {
		return ApprovalView{}, ErrInvalidArgument
	}
	if err := transaction(tx, tenant, id); err != nil {
		return ApprovalView{}, err
	}
	tx = tx.Session(&gorm.Session{Logger: logger.Discard})
	row, err := m.lockApprovalRunner(ctx, tx, tenant, id, f, in.DispatchEpoch)
	if err != nil {
		return ApprovalView{}, err
	}
	v, err := view(row)
	if err != nil {
		return ApprovalView{}, err
	}
	principal := v.Plan.ExecutorPrincipalID
	if principal <= 0 {
		principal = row.InitiatorID
	}
	if m.policy == nil {
		return ApprovalView{}, ErrUnavailable
	}
	scope, err := m.policy(ctx, tx, tenant, row.ProjectID, "interaction_request", principal)
	if err != nil {
		return ApprovalView{}, err
	}
	if !scope.AllowsEntityRows(row.ProjectID) || (!scope.AllowAll && !slices.Contains(scope.ResourceIDs, row.ProjectID.String()) && !slices.Contains(scope.OwnerIDs, principal.String())) {
		return ApprovalView{}, ErrForbidden
	}
	encoded, _ := json.Marshal(in)
	digest := hash(encoded)
	q := query.BindApprovals(tx, m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.DispatchEpoch.Eq(in.DispatchEpoch), q.RequestID.Eq(in.RequestID)).First()
	if err == nil {
		if prior.RequestHash != digest || prior.SessionID != f.SessionID || prior.ConnEpoch != f.ConnEpoch {
			return ApprovalView{}, ErrConflict
		}
		return approvalView(prior), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ApprovalView{}, err
	}
	// Flooding never turns a runtime into an unbounded inbox producer.
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.DispatchEpoch.Eq(in.DispatchEpoch)).Count()
	if err != nil {
		return ApprovalView{}, err
	}
	if count >= 100 {
		return ApprovalView{}, ErrConflict
	}
	key, err := m.nextID()
	if err != nil {
		return ApprovalView{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.Approval{ID: key, TenantID: tenant, ExecutionID: id, DispatchEpoch: in.DispatchEpoch, SessionID: f.SessionID, ConnEpoch: f.ConnEpoch, RequestID: in.RequestID, Tool: in.Tool, InputJSON: string(in.Input), RequestHash: digest, State: "PENDING", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if err = q.WithContext(ctx).Create(r); err != nil {
		return ApprovalView{}, err
	}
	// Request input is intentionally excluded from audit/realtime payloads.
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"approval_id": key.String(), "approval_state": "PENDING"}); err != nil {
		return ApprovalView{}, err
	}
	return approvalView(r), nil
}

func (m *Module) ReadRunnerApprovalOn(ctx context.Context, tx *gorm.DB, tenant, id, approval guid.ID, f RunnerFence, epoch int64) (ApprovalView, error) {
	if _, err := m.lockApprovalRunner(ctx, tx, tenant, id, f, epoch); err != nil {
		return ApprovalView{}, err
	}
	q := query.BindApprovals(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.ID.Eq(approval), q.DispatchEpoch.Eq(epoch), q.SessionID.Eq(f.SessionID), q.ConnEpoch.Eq(f.ConnEpoch)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ApprovalView{}, ErrNotFound
	}
	if err != nil {
		return ApprovalView{}, err
	}
	return approvalView(r), nil
}

func (m *Module) DecideApprovalOn(ctx context.Context, tx *gorm.DB, tenant, id, approval guid.ID, state, reason string) (ApprovalView, error) {
	if (state != "ALLOW" && state != "DENY") || !bounded(strings.TrimSpace(reason), 2000) {
		return ApprovalView{}, ErrInvalidArgument
	}
	snap, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return ApprovalView{}, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err = m.workPrincipalLocks(ctx, tx, link(snap, actor), actor, snap.InitiatorID); err != nil {
		return ApprovalView{}, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, snap.ProjectID, "interaction_respond"); err != nil {
		return ApprovalView{}, err
	}
	if m.work.AuthorizeOn == nil {
		return ApprovalView{}, ErrUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(snap, actor), "RECEIPT"); err != nil {
		return ApprovalView{}, err
	}
	row, err := m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return ApprovalView{}, err
	}
	q := query.BindApprovals(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(id), q.ID.Eq(approval)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ApprovalView{}, ErrNotFound
	}
	if err != nil {
		return ApprovalView{}, err
	}
	if row.State != "RUNNING" || row.CancelRequestedAt != nil || row.DispatchEpoch != r.DispatchEpoch || !r.ExpiresAt.After(time.Now()) || row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(time.Now()) {
		return ApprovalView{}, ErrFenced
	}
	if r.State != "PENDING" {
		if r.State != state || r.Reason != reason || r.ActorID == nil || *r.ActorID != actor {
			return ApprovalView{}, ErrConflict
		}
		return approvalView(r), nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID)).Updates(map[string]any{"state": state, "actor_id": actor, "reason": reason, "decided_at": now}); err != nil {
		return ApprovalView{}, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "IAM", map[string]any{"approval_id": r.ID.String(), "approval_state": state, "reason": reason}); err != nil {
		return ApprovalView{}, err
	}
	r.State, r.ActorID, r.Reason, r.DecidedAt = state, &actor, reason, &now
	return approvalView(r), nil
}

func validPlanApprovalProtocol(p ExecutionPlan) bool {
	return (p.Runtime == "CLAUDE_CODE" && p.ApprovalProtocol == approvalProtocol) || (p.Runtime == "CODEX" && p.ApprovalProtocol == "CODEX_APP_SERVER_V1")
}
