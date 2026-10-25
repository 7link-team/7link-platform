package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// RuntimeControl is bound by a trusted machine adapter. RequestOn must verify
// current machine connectivity and no active executions, then enqueue a durable
// runner command in the caller's transaction. Missing integration fails closed.
type RuntimeControl struct {
	PrepareOn func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	RequestOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error
}
type RuntimeControlConnector struct{ Bind func(RuntimeControl) error }
type RuntimeUpdates struct {
	ReportOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, int, string) error
	CompleteResetOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error
}
type ResetInput struct {
	Kind        string `json:"kind" enum:"RESTART,SESSION,FULL"`
	Reason      string `json:"reason" minLength:"10" maxLength:"2000"`
	ConfirmName string `json:"confirm_name,omitempty" maxLength:"200"`
	ReauthProof string `json:"reauth_proof,omitempty" maxLength:"512"`
}
type ResetView struct {
	ID        guid.ID   `json:"id"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	Next      []Next    `json:"next"`
}

type ResetHistoryView struct {
	ResetView
	Reason      string  `json:"reason"`
	RequestedBy guid.ID `json:"requested_by"`
}

// Reset history reports persisted command outcomes, never inferred process state.
func (m *Module) resetHistoryOn(ctx context.Context, tx *gorm.DB, tenant, agent, before guid.ID, limit int) (Page[ResetHistoryView], error) {
	out := Page[ResetHistoryView]{Items: []ResetHistoryView{}, Next: []Next{{Operation: "agent_protocol.live"}}}
	if before < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	permission := "agent_workspace_read"
	if actorID(ctx) == agent {
		permission = "SELF"
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, permission, false); err != nil {
		return out, err
	}
	q := query.BindReset(tx, m.names)
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent))
	if before > 0 {
		r = r.Where(q.ID.Lt(before))
	}
	rows, err := r.Order(q.ID.Desc()).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
		out.NextCursor = rows[len(rows)-1].ID.String()
	}
	for _, row := range rows {
		out.Items = append(out.Items, ResetHistoryView{ResetView: ResetView{ID: row.ID, Kind: row.Kind, State: row.State, CreatedAt: row.CreatedAt, Next: []Next{{Operation: "agent_protocol.live"}}}, Reason: row.Reason, RequestedBy: row.RequestedBy})
	}
	return out, nil
}

func (m *Module) bindRuntime(c RuntimeControl) error {
	if c.RequestOn == nil || m.runtimeControl.RequestOn != nil {
		return ErrInvalid
	}
	m.runtimeControl = c
	return nil
}
func (m *Module) reportOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, epoch int, activity string) error {
	if !slices.Contains([]string{"ONLINE", "THINKING", "WORKING", "ERROR", "OFFLINE"}, activity) || epoch < 1 || tenant <= 0 || agent <= 0 {
		return ErrInvalid
	}
	if m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	if !found || a.PrincipalID != agent || a.TenantID != tenant {
		return ErrNotFound
	}
	r, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	if r.SessionEpoch != epoch {
		return ErrConflict
	}
	if r.Activity == activity {
		return nil
	}
	q := query.BindLiveState(tx, m.names)
	now := m.now()
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.SessionEpoch.Eq(epoch)).Updates(map[string]any{"activity": activity, "updated_at": now})
	if err != nil {
		return err
	}
	if res.RowsAffected != 1 {
		return ErrConflict
	}
	if m.activityObserver != nil {
		return m.activityObserver(ctx, tx, ActivityTransition{TenantID: tenant, AgentPrincipalID: agent, ResponsiblePrincipalID: a.OwnerPrincipalID, Name: a.Name, Previous: r.Activity, Activity: activity, SessionEpoch: epoch, OccurredAt: now})
	}
	return nil
}
func (m *Module) resetOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in ResetInput) (ResetView, error) {
	var out ResetView
	if !slices.Contains([]string{"RESTART", "SESSION", "FULL"}, in.Kind) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalid
	}
	a, err := m.authorize(ctx, tx, tenant, agent, "agent_manage", true)
	if err != nil {
		return out, err
	}
	if m.runtimeControl.RequestOn == nil || m.ids == nil {
		return out, ErrUnavailable
	}
	if in.Kind == "FULL" {
		if in.ConfirmName != a.Name {
			return out, ErrInvalid
		}
		if in.ReauthProof == "" || m.fullResetProof == nil {
			return out, ErrForbidden
		}
		if err = m.fullResetProof(ctx, tx, tenant, agent, in.ReauthProof); err != nil {
			return out, ErrForbidden
		}
	}
	if m.runtimeControl.PrepareOn != nil {
		if err = m.runtimeControl.PrepareOn(ctx, tx, tenant, agent); err != nil {
			return out, resetRuntimeError(err)
		}
	}
	state, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return out, err
	}
	q := query.BindReset(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.State.Eq("REQUESTED")).Count()
	if err != nil {
		return out, err
	}
	if count > 0 {
		return out, ErrConflict
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	session, workspace := state.SessionEpoch, state.WorkspaceEpoch
	if in.Kind != "RESTART" {
		session++
	}
	if in.Kind == "FULL" {
		workspace++
	}
	if err = m.runtimeControl.RequestOn(ctx, tx, tenant, agent, guid.ID(id), in.Kind, session, workspace); err != nil {
		return out, resetRuntimeError(err)
	}
	row := model.Reset{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: agent, Kind: in.Kind, State: "REQUESTED", Reason: strings.TrimSpace(in.Reason), RequestedBy: actorID(ctx), CreatedAt: m.now()}
	if err = q.WithContext(ctx).Create(&row); err != nil {
		return out, err
	}
	if err = m.record(ctx, tx, tenant, agent, "RESET_REQUESTED", true); err != nil {
		return out, err
	}
	if in.Kind == "RESTART" {
		if m.profiles.RearmOn != nil {
			if err = m.profiles.RearmOn(ctx, tx, tenant, agent); err != nil {
				return out, resetRuntimeError(err)
			}
		}
		if err = m.observeProcessOn(ctx, tx, fleet.AgentProcessTransition{TenantID: tenant, AgentPrincipalID: agent, Kind: "RESTART_REQUESTED", Reason: in.Reason, OccurredAt: m.now()}); err != nil {
			return out, err
		}
	}
	// Epochs change only on confirmed completion, never on a queued request.
	return ResetView{ID: row.ID, Kind: row.Kind, State: row.State, CreatedAt: row.CreatedAt, Next: []Next{{Operation: "agent_protocol.live"}}}, nil
}

func resetRuntimeError(err error) error {
	switch {
	case errors.Is(err, fleet.ErrVersionConflict), errors.Is(err, fleet.ErrMachineOffline), errors.Is(err, fleet.ErrSessionStale):
		return ErrConflict
	case errors.Is(err, fleet.ErrMachineForbidden), errors.Is(err, fleet.ErrMachineRevoked):
		return ErrForbidden
	case errors.Is(err, fleet.ErrMachineNotFound):
		return ErrNotFound
	case errors.Is(err, fleet.ErrUnavailable):
		return ErrUnavailable
	default:
		return err
	}
}
func (m *Module) completeResetOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID, state string) error {
	if state != "DONE" && state != "FAILED" && state != "TIMED_OUT" {
		return ErrInvalid
	}
	live, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	q := query.BindReset(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if row.State == state {
		return nil
	}
	if row.State != "REQUESTED" {
		return ErrConflict
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.State.Eq("REQUESTED")).Update(q.State, state)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	if state == "DONE" {
		session, workspace := live.SessionEpoch, live.WorkspaceEpoch
		if row.Kind != "RESTART" {
			session++
		}
		if row.Kind == "FULL" {
			workspace++
		}
		l := query.BindLiveState(tx, m.names)
		_, err = l.WithContext(ctx).Where(l.TenantID.Eq(tenant), l.AgentPrincipalID.Eq(agent)).Updates(map[string]any{"session_epoch": session, "workspace_epoch": workspace, "activity": "OFFLINE", "updated_at": m.now()})
		if err != nil {
			return err
		}
	}
	_, err = m.appendOn(ctx, tx, tenant, agent, InboxReference{SourceEventID: "reset:" + id.String() + ":" + state, Kind: "STATE", SubjectID: id})
	return err
}
