package fleet

import (
	"context"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type ResetCommandView struct {
	ID               guid.ID   `json:"id"`
	AgentPrincipalID guid.ID   `json:"agent_principal_id"`
	MachineID        guid.ID   `json:"machine_id"`
	Kind             string    `json:"kind" enum:"RESTART,SESSION,FULL"`
	State            string    `json:"state" enum:"QUEUED,RUNNING,DONE,FAILED,TIMED_OUT"`
	SessionEpoch     int       `json:"session_epoch"`
	WorkspaceEpoch   int       `json:"workspace_epoch"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func resetCommandView(r *model.ResetCommand) ResetCommandView {
	return ResetCommandView{r.ID, r.AgentPrincipalID, r.MachineID, r.Kind, r.State, r.SessionEpoch, r.WorkspaceEpoch, r.ExpiresAt}
}

func (m *Module) bindResetCompletion(f func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error) error {
	if f == nil || m.resetComplete != nil {
		return ErrInvalidArgument
	}
	m.resetComplete = f
	return nil
}

// The caller has authorized agent_manage. Acquire the machine before the
// agent's live-state lock, matching receipt and watchdog lock order.
func (m *Module) prepareAgentResetOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) error {
	if err := fleetTransaction(tx, tenant, agent); err != nil {
		return err
	}
	q := query.BindAgents(tx, m.names)
	a, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrMachineNotFound
	}
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"ACTIVE", "STOPPED", "CRASHED"}, a.State) || a.MachineID == nil {
		return ErrVersionConflict
	}
	row, err := m.machineOn(ctx, tx, tenant, *a.MachineID, false)
	if err != nil {
		return err
	}
	if m.principals.Lock == nil || m.accounts == nil {
		return ErrUnavailable
	}
	if err = m.principals.Lock(ctx, tx, row.PrincipalID); err != nil {
		return err
	}
	valid, err := m.accounts(ctx, tx, tenant, row.PrincipalID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !valid {
		return ErrMachineForbidden
	}
	row, err = m.machineOn(ctx, tx, tenant, row.ID, true)
	if err != nil {
		return err
	}
	if machineState(row, time.Now().UTC()) != "ONLINE" || row.CurrentSessionID == nil {
		return ErrVersionConflict
	}
	slots := query.BindSlots(tx, m.names)
	count, err := slots.WithContext(ctx).Where(slots.TenantID.Eq(tenant), slots.MachineID.Eq(row.ID), slots.ActiveSlot.IsNotNull()).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrVersionConflict
	}
	return nil
}

func (m *Module) enqueueAgentResetOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID, kind string, session, workspace int) error {
	if id <= 0 || session < 1 || workspace < 1 || !slices.Contains([]string{"RESTART", "SESSION", "FULL"}, kind) {
		return ErrInvalidArgument
	}
	if m.resetComplete == nil {
		return ErrUnavailable
	}
	if err := m.prepareAgentResetOn(ctx, tx, tenant, agent); err != nil {
		return err
	}
	a := query.BindAgents(tx, m.names)
	row, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.PrincipalID.Eq(agent)).First()
	if err != nil {
		return err
	}
	machine, err := m.machineOn(ctx, tx, tenant, *row.MachineID, true)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	command := &model.ResetCommand{ID: id, TenantID: tenant, AgentPrincipalID: agent, MachineID: machine.ID, SessionID: *machine.CurrentSessionID, ConnEpoch: machine.ConnEpoch, SessionEpoch: session, WorkspaceEpoch: workspace, Kind: kind, State: "QUEUED", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	return query.BindResetCommands(tx, m.names).WithContext(ctx).Create(command)
}

// Pull is replayable within a connection; the runner persists each receipt
// before sending it and never repeats an action after an uncertain restart.
func (m *Module) pullAgentResetOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) ([]ResetCommandView, error) {
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return nil, err
	}
	q := query.BindResetCommands(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.SessionID.Eq(session), q.ConnEpoch.Eq(epoch), q.State.In("QUEUED", "RUNNING"), q.ExpiresAt.Gt(time.Now().UTC())).Order(q.ID).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	out := []ResetCommandView{}
	for _, row := range rows {
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Update(q.State, "RUNNING"); err != nil {
			return nil, err
		}
		row.State = "RUNNING"
		out = append(out, resetCommandView(row))
	}
	return out, nil
}

func (m *Module) reportAgentResetOn(ctx context.Context, tx *gorm.DB, tenant, machine, id, session guid.ID, epoch int64, state, reason string) (ResetCommandView, error) {
	var empty ResetCommandView
	if id <= 0 || (state != "DONE" && state != "FAILED") || !slices.Contains([]string{"", "UNSUPPORTED_AGENT_RUNTIME", "RESET_FAILED", "OUTCOME_UNKNOWN"}, reason) || (state == "DONE" && reason != "") || (state == "FAILED" && reason == "") {
		return empty, ErrInvalidArgument
	}
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return empty, err
	}
	q := query.BindResetCommands(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrMachineNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.SessionID != session || row.ConnEpoch != epoch {
		return empty, ErrSessionStale
	}
	if row.State == state && row.ReasonCode == reason {
		return resetCommandView(row), nil
	}
	if row.State != "RUNNING" || !row.ExpiresAt.After(time.Now().UTC()) {
		return empty, ErrVersionConflict
	}
	if m.resetComplete == nil {
		return empty, ErrUnavailable
	}
	if err = m.resetComplete(ctx, tx, tenant, row.AgentPrincipalID, id, state); err != nil {
		return empty, err
	}
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": state, "reason_code": reason}); err != nil {
		return empty, err
	}
	row.State, row.ReasonCode = state, reason
	return resetCommandView(row), nil
}

func (m *Module) expireAgentResets(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindResetCommands(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("QUEUED", "RUNNING")).Order(q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			machine, e := m.machineOn(ctx, tx, tenant, row.MachineID, true)
			if e != nil {
				return e
			}
			if row.ExpiresAt.After(now) && machine.ConnEpoch == row.ConnEpoch && machine.State != "REVOKED" {
				return nil
			}
			q := query.BindResetCommands(tx, m.names)
			current, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).First()
			if e != nil {
				return e
			}
			if current.State != "QUEUED" && current.State != "RUNNING" {
				return nil
			}
			if m.resetComplete == nil {
				return ErrUnavailable
			}
			if e = m.resetComplete(ctx, tx, tenant, row.AgentPrincipalID, row.ID, "TIMED_OUT"); e != nil {
				return e
			}
			_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Update(q.State, "TIMED_OUT")
			return e
		})
		if err != nil {
			return err
		}
	}
	return nil
}
