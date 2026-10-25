package fleet

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// MachineLoad is slot reservation load, not inferred CPU utilization or
// execution success. Offline machines retain real reservations until released.
type MachineLoad struct {
	ID         guid.ID    `json:"id"`
	Name       string     `json:"name"`
	State      string     `json:"state" enum:"ONLINE,OFFLINE,DRAINING,REVOKED"`
	Slots      int        `json:"slots"`
	Reserved   int64      `json:"reserved"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

type WorkbenchMachines func(context.Context, *gorm.DB, guid.ID) ([]MachineLoad, error)

func (m *Module) provideWorkbenchFacts(s *module.Services) error {
	if err := module.Provide[MachineOfflineMachines](s, m.machineOfflineMachinesOn); err != nil {
		return err
	}
	if err := module.Provide[MachineNoticeFacts](s, m.machineNoticeFactsOn); err != nil {
		return err
	}
	if err := module.Provide[EnrollmentNotificationFacts](s, m.enrollmentNotificationFactsOn); err != nil {
		return err
	}
	if err := module.Provide[WorkbenchCapacity](s, m.workbenchCapacityOn); err != nil {
		return err
	}
	if err := module.Provide[GatewayRotationFacts](s, m.gatewayRotationFactOn); err != nil {
		return err
	}
	if err := module.Provide[AgentRegistrationCommands](s, AgentRegistrationCommands{RegisterOn: m.RegisterAgentOn}); err != nil {
		return err
	}
	return module.Provide[WorkbenchMachines](s, m.workbenchMachinesOn)
}

// MachineNoticeFact uses the immutable closed runner session as the outage
// episode. Unconnected enrollment is handled by its separate ten-minute notice.
type MachineNoticeFact struct {
	MachineID, SessionID guid.ID
	OfflineAt            time.Time
	Offline, Recovered   bool
	Owners, Executions   []guid.ID
}

type MachineNoticeFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (MachineNoticeFact, bool, error)

type MachineOfflineMachines func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error)

func (m *Module) machineOfflineMachinesOn(ctx context.Context, tx *gorm.DB, tenant, after guid.ID, limit int) ([]guid.ID, error) {
	if tx == nil || tenant <= 0 || after < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidArgument
	}
	q := query.BindMachines(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.State.Eq("OFFLINE"), q.CurrentSessionID.IsNotNull()).Order(q.ID).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	out := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

func (m *Module) machineNoticeFactsOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID) (MachineNoticeFact, bool, error) {
	var out MachineNoticeFact
	if tx == nil || tenant <= 0 || machine < 0 || session < 0 || (machine == 0 && session == 0) {
		return out, false, ErrInvalidArgument
	}
	if machine == 0 {
		s := query.BindSessions(tx, m.names)
		r, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.ID.Eq(session)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		machine = r.MachineID
	}
	q := query.BindMachines(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(machine)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil || row.State == "REVOKED" || row.CurrentSessionID == nil {
		return out, false, err
	}
	if session == 0 {
		session = *row.CurrentSessionID
	}
	s := query.BindSessions(tx, m.names)
	old, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.MachineID.Eq(machine), s.ID.Eq(session), s.State.In("EXPIRED", "SUPERSEDED"), s.ClosedAt.IsNotNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.MachineID, out.SessionID, out.OfflineAt = machine, session, old.LastSeenAt
	out.Offline = *row.CurrentSessionID == session && row.State == "OFFLINE"
	out.Recovered = row.ConnEpoch > old.ConnEpoch && (machineState(row, time.Now().UTC()) == "ONLINE" || machineState(row, time.Now().UTC()) == "DRAINING")
	if !out.Offline && !out.Recovered {
		return out, false, nil
	}
	a := query.BindAgents(tx, m.names)
	agents, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.MachineID.Eq(machine), a.State.Eq("ACTIVE")).Find()
	if err != nil {
		return out, false, err
	}
	for _, agent := range agents {
		if agent.OwnerPrincipalID > 0 {
			out.Owners = append(out.Owners, agent.OwnerPrincipalID)
		}
	}
	// Slot ownership at the last actual heartbeat identifies affected work;
	// unrelated old executions never qualify by a mutable update timestamp.
	r := query.BindSlots(tx, m.names)
	reservations, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.MachineID.Eq(machine), r.CreatedAt.Lte(old.LastSeenAt)).Find()
	if err != nil {
		return out, false, err
	}
	for _, slot := range reservations {
		if slot.ReleasedAt == nil || !slot.ReleasedAt.Before(old.LastSeenAt) {
			out.Executions = append(out.Executions, slot.ExecutionID)
		}
	}
	return out, true, nil
}

type EnrollmentNotificationFact struct {
	MachineID, ApproverID guid.ID
	ApprovedAt            time.Time
	EverOnline            bool
}

type EnrollmentNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (EnrollmentNotificationFact, bool, error)

func (m *Module) enrollmentNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (EnrollmentNotificationFact, bool, error) {
	var out EnrollmentNotificationFact
	if tx == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindEnrollments(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if r.ApprovedBy == nil || r.MachineID == nil || *r.ApprovedBy <= 0 || *r.MachineID <= 0 || r.State == "FAILED" || r.State == "DENIED" || r.State == "COMPENSATING" {
		return out, false, nil
	}
	machines := query.BindMachines(tx, m.names)
	machine, err := machines.WithContext(ctx).Where(machines.TenantID.Eq(tenant), machines.ID.Eq(*r.MachineID), machines.State.Neq("REVOKED")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	// Canonical enrollment creates the machine and APPROVED receipt in one
	// transaction; CreatedAt remains immutable through issuance/acknowledgment.
	out.MachineID, out.ApproverID, out.ApprovedAt = *r.MachineID, *r.ApprovedBy, machine.CreatedAt
	sessions := query.BindSessions(tx, m.names)
	n, err := sessions.WithContext(ctx).Where(sessions.TenantID.Eq(tenant), sessions.MachineID.Eq(machine.ID)).Count()
	if err != nil {
		return out, false, err
	}
	out.EverOnline = n > 0 || machine.LastSeenAt != nil
	return out, true, nil
}

// WorkbenchCapacity exposes the real configured Agent concurrency cap only to
// current Agent inventory readers. Human capacity has no persisted source here.
type WorkbenchCapacity func(context.Context, *gorm.DB, guid.ID, guid.ID) (*int, error)

func (m *Module) workbenchCapacityOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (*int, error) {
	if m == nil || m.policy == nil {
		return nil, ErrUnavailable
	}
	if tx == nil || tenant <= 0 || principal <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, ErrMachineForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	c, err := m.policy(ctx, tx, tenant, "agent_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if !c.AllowAll {
		return nil, nil
	}
	q := query.BindAgents(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.State.Eq("ACTIVE")).Limit(1).Find()
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	if rows[0].MaxParallelItems <= 0 {
		return nil, ErrInvalidArgument
	}
	v := rows[0].MaxParallelItems
	return &v, nil
}

// AgentRegistrationCommands retains the existing Human-authorized service
// account registration. Autonomous creation is a separate source capability;
// a request must never impersonate a Human to call RegisterOn.
type AgentRegistrationCommands struct {
	RegisterOn func(context.Context, *gorm.DB, guid.ID, AgentRegistration) (RegisteredAgent, error)
}

func (m *Module) workbenchMachinesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]MachineLoad, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, ErrMachineForbidden
	}
	if m == nil || m.policy == nil {
		return nil, ErrUnavailable
	}
	// Match the canonical machine inventory's current IAM boundary. A denied
	// caller receives no machine identities, names or counts.
	c, err := m.policy(ctx, tx, tenant, "machine_read", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if !c.AllowAll {
		return nil, ErrMachineForbidden
	}
	out := []MachineLoad{}
	q := query.BindMachines(tx, m.names)
	var cursor guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			cursor = row.ID
			s := query.BindSlots(tx, m.names)
			reserved, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.MachineID.Eq(row.ID), s.ActiveSlot.IsNotNull(), s.ReleasedAt.IsNull()).Count()
			if err != nil {
				return nil, err
			}
			out = append(out, MachineLoad{ID: row.ID, Name: row.Name, State: machineState(row, time.Now().UTC()), Slots: row.SlotCount, Reserved: reserved, LastSeenAt: row.LastSeenAt})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
}
