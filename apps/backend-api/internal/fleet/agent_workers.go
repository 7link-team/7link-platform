package fleet

import (
	"context"
	"database/sql/driver"
	"net/http"
	"slices"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentWorker struct {
	AgentPrincipalID guid.ID  `json:"agent_principal_id"`
	Runtime          string   `json:"runtime"`
	Model            string   `json:"model"`
	CredentialID     *guid.ID `json:"credential_id,omitempty"`
	ConfigRevision   int      `json:"config_revision,omitempty"`
	LaunchVersion    int      `json:"launch_version,omitempty"`
	RestartRequested bool     `json:"restart_requested,omitempty"`
}
type AgentPresence struct {
	AgentPrincipalID guid.ID `json:"agent_principal_id"`
	PID              int     `json:"pid" minimum:"0"`
	State            string  `json:"state" enum:"RUNNING,EXITED"`
	ConfigRevision   *int    `json:"config_revision,omitempty" minimum:"1"`
	LaunchVersion    int     `json:"launch_version,omitempty" minimum:"0"`
	Attempted        bool    `json:"attempted,omitempty"`
}
type AgentPresenceSnapshot struct {
	SessionID guid.ID         `json:"session_id"`
	ConnEpoch int64           `json:"conn_epoch" minimum:"1"`
	Agents    []AgentPresence `json:"agents" maxItems:"100"`
}

// agentWorkersOn locks all relevant principals before the session's machine
// lock. Rows are profile assignments, never proof of a running process.
func (m *Module) agentWorkersOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) ([]*model.Agent, error) {
	if fleetTransaction(tx, tenant, machine) != nil {
		return nil, ErrInvalidArgument
	}
	if m.principals.Lock == nil {
		return nil, ErrUnavailable
	}
	q := query.BindAgents(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.In("ACTIVE", "CRASHED", "STOPPED")).Order(q.PrincipalID).Limit(101).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 100 {
		return nil, ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx)}
	for _, row := range rows {
		ids = append(ids, row.PrincipalID)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return nil, ErrMachineForbidden
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	for _, row := range rows {
		if m.accounts == nil {
			return nil, ErrUnavailable
		}
		active, err := m.accounts(ctx, tx, tenant, row.PrincipalID, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, ErrMachineForbidden
		}
	}
	if err = m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []*model.Agent{}, nil
	}
	prelocked := make([]driver.Valuer, len(rows))
	for i, row := range rows {
		prelocked[i] = row.PrincipalID
	}
	// Registration may add another profile before the machine lock. Defer that
	// profile to the next sync rather than acquiring its principal out of order.
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.In("ACTIVE", "CRASHED", "STOPPED"), q.PrincipalID.In(prelocked...)).Order(q.PrincipalID).Limit(101).Find()
}

func (m *Module) AgentWorkersOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) ([]AgentWorker, error) {
	rows, err := m.agentWorkersOn(ctx, tx, tenant, machine, session, epoch)
	if err != nil {
		return nil, err
	}
	out := []AgentWorker{}
	for _, row := range rows {
		if !agentWorkerDesired(row, time.Now().UTC()) {
			continue
		}
		if !agentBindingReady(row) {
			continue
		}
		profile := AgentWorker{AgentPrincipalID: row.PrincipalID, Runtime: row.RuntimeKind, Model: row.ModelID, CredentialID: row.CredentialID, LaunchVersion: row.Version, ConfigRevision: row.ConfigRevision, RestartRequested: row.ProcessStatus == "STARTING"}
		out = append(out, profile)
	}
	return out, nil
}

// ReportAgentPresenceOn records a full snapshot of actual supervised child
// processes. PID is preserved in audit/outbox evidence; it is not inferred from
// installed runtime inventory. Missing children become EXITED atomically.
func (m *Module) ReportAgentPresenceOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, in AgentPresenceSnapshot) error {
	if len(in.Agents) > 100 {
		return ErrInvalidArgument
	}
	reports := map[guid.ID]AgentPresence{}
	for _, item := range in.Agents {
		if item.AgentPrincipalID <= 0 || !slices.Contains([]string{"RUNNING", "EXITED"}, item.State) || item.PID < 0 || item.State == "RUNNING" && item.PID <= 0 || item.LaunchVersion < 0 || item.ConfigRevision != nil && *item.ConfigRevision < 1 || item.Attempted && item.LaunchVersion < 1 {
			return ErrInvalidArgument
		}
		if _, ok := reports[item.AgentPrincipalID]; ok {
			return ErrInvalidArgument
		}
		reports[item.AgentPrincipalID] = item
	}
	rows, err := m.agentWorkersOn(ctx, tx, tenant, machine, in.SessionID, in.ConnEpoch)
	if err != nil {
		return err
	}
	allowed := map[guid.ID]bool{}
	for _, row := range rows {
		allowed[row.PrincipalID] = agentBindingReady(row)
	}
	for id := range reports {
		if !allowed[id] {
			return ErrMachineForbidden
		}
	}
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindAgents(tx, m.names)
	for _, row := range rows {
		report, ok := reports[row.PrincipalID]
		if !ok {
			report = AgentPresence{AgentPrincipalID: row.PrincipalID, State: "EXITED"}
		}
		if report.LaunchVersion > 0 && report.LaunchVersion != row.Version && !(report.LaunchVersion < row.Version && row.State == "ACTIVE" && row.ProcessStatus == "RUNNING" && report.ConfigRevision != nil && *report.ConfigRevision < row.ConfigRevision && row.RunningConfigRevision != nil && *report.ConfigRevision == *row.RunningConfigRevision) {
			continue
		}
		if report.Attempted && row.State == "CRASHED" && !agentWorkerDesired(row, now) {
			continue
		}
		if report.ConfigRevision != nil && *report.ConfigRevision > row.ConfigRevision {
			return ErrVersionConflict
		}
		// The stop request can race one final live snapshot; the next desired
		// set removes the worker and only its later absence confirms the stop.
		if row.State == "STOPPED" && report.State == "RUNNING" {
			continue
		}
		changes := map[string]any{"process_status": report.State, "updated_at": now}
		transition := row.ProcessStatus != report.State
		if transition {
			changes["process_status_at"] = now
		}
		if report.State == "RUNNING" {
			changes["last_seen_at"] = now
			changes["running_config_revision"] = report.ConfigRevision
			if row.State == "CRASHED" {
				changes["state"] = "ACTIVE"
			}
		}
		if report.State == "EXITED" && (row.ProcessStatus == "RUNNING" && row.State == "ACTIVE" || report.Attempted && (row.State == "ACTIVE" || row.State == "CRASHED")) {
			count := row.RestartCountInWindow
			if row.RestartWindowStartedAt == nil || now.Sub(*row.RestartWindowStartedAt) >= 30*time.Minute {
				count = 0
				changes["restart_window_started_at"] = now
			}
			count++
			exhausted := count > 3
			changes["restart_count_in_window"], changes["restart_exhausted"], changes["state"] = min(3, count), exhausted, "CRASHED"
			changes["version"] = row.Version + 1
			changes["running_config_revision"] = nil
			changes["process_status_at"] = now
			typ := "agent.crashed"
			if exhausted {
				typ = "agent.restart_exhausted"
			}
			if _, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: typ, Subject: events.Subject{Kind: "agent", ID: row.PrincipalID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: "MACHINE", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"agent_id": row.ID, "principal_id": row.PrincipalID, "machine_id": machine, "restart_count": count, "restart_exhausted": exhausted}}); err != nil {
				return err
			}
			if m.processObserver != nil {
				kind := "CRASHED"
				if exhausted {
					kind = "RESTART_EXHAUSTED"
				}
				if err = m.processObserver(ctx, tx, AgentProcessTransition{TenantID: tenant, AgentPrincipalID: row.PrincipalID, MachineID: row.MachineID, Kind: kind, RestartCount: count, OccurredAt: now}); err != nil {
					return err
				}
				if row.AutoRestart && !exhausted {
					if err = m.processObserver(ctx, tx, AgentProcessTransition{TenantID: tenant, AgentPrincipalID: row.PrincipalID, MachineID: row.MachineID, Kind: "AUTO_RESTART_SCHEDULED", RestartCount: count, OccurredAt: now}); err != nil {
						return err
					}
				}
			}
		} else if transition && m.processObserver != nil {
			kind := "STOPPED"
			if report.State == "RUNNING" {
				kind = "STARTED"
			}
			if err = m.processObserver(ctx, tx, AgentProcessTransition{TenantID: tenant, AgentPrincipalID: row.PrincipalID, MachineID: row.MachineID, Kind: kind, PID: report.PID, RestartCount: row.RestartCountInWindow, OccurredAt: now}); err != nil {
				return err
			}
		}
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Updates(changes); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, tenant, "fleet.agent_presence", "agent", row.PrincipalID)
		event.Detail["machine_id"], event.Detail["session_id"], event.Detail["conn_epoch"], event.Detail["pid"], event.Detail["process_status"] = machine.String(), in.SessionID.String(), in.ConnEpoch, report.PID, report.State
		if err = m.audit(ctx, tx, event); err != nil {
			return err
		}
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "fleet.agent_presence", Subject: events.Subject{Kind: "agent", ID: row.PrincipalID}, SubjectVersion: int64(row.Version), Actor: events.Actor{Kind: "MACHINE", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"process_status": report.State, "machine_id": machine.String(), "pid": report.PID}})
		if err != nil {
			return err
		}
	}
	return nil
}

// Delays apply only to actual exits observed in this current machine fence.
func agentWorkerDesired(row *model.Agent, now time.Time) bool {
	if row.State == "ACTIVE" {
		return true
	}
	if row.State != "CRASHED" || !row.AutoRestart || row.RestartExhausted || row.ProcessStatusAt == nil {
		return false
	}
	delay := 10 * time.Second
	switch row.RestartCountInWindow {
	case 2:
		delay = time.Minute
	case 3:
		delay = 5 * time.Minute
	}
	return !now.Before(row.ProcessStatusAt.Add(delay))
}

type agentWorkersInput struct {
	MachineInput
	SessionID string `query:"session_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ConnEpoch int64  `query:"conn_epoch" required:"true" minimum:"1"`
}
type agentPresenceInput struct {
	MachineInput
	Body AgentPresenceSnapshot
}

func (m *Module) registerAgentWorkersHTTP(api huma.API) error {
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.agent-workers.list", Method: http.MethodGet, Path: "/machines/{machine_id}/agent-workers", Summary: "List current Agent profiles bound to a fenced runner", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.agentWorkersHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.agent-presences.replace", Method: http.MethodPost, Path: "/machines/{machine_id}/agent-presences", Summary: "Report actual supervised Agent process snapshot", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.agentPresenceHTTP)
	return authz.RegisterRequestContract(api, "/machines/{machine_id}/agent-presences")
}
func (m *Module) agentWorkersHTTP(ctx context.Context, in *agentWorkersInput) (*respx.Body[[]AgentWorker], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.BindRunnerContext(ctx, id)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out []AgentWorker
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		session, parseErr := guid.RequireID(in.SessionID)
		if parseErr != nil {
			return ErrInvalidArgument
		}
		out, e = m.AgentWorkersOn(ctx, tx, authn.TenantIDFromContext(ctx), id, session, in.ConnEpoch)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) agentPresenceHTTP(ctx context.Context, in *agentPresenceInput) (*respx.Body[bool], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.BindRunnerContext(ctx, id)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.ReportAgentPresenceOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body)
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}
