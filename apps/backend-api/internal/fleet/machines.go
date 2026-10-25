package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"math"
	"strings"
	"time"
	"unicode"
)

var (
	ErrMachineNotFound  = errors.New("fleet: machine not found")
	ErrMachineOffline   = errors.New("fleet: machine is offline")
	ErrMachineDraining  = errors.New("fleet: machine is draining")
	ErrMachineRevoked   = errors.New("fleet: machine is revoked")
	ErrMachineForbidden = errors.New("fleet: machine principal is not active")
	ErrSessionStale     = errors.New("fleet: stale runner session")
	ErrSessionActive    = errors.New("fleet: another runner session is active")
	ErrSlotsFull        = errors.New("fleet: no available execution slot")
	ErrSlotReleased     = errors.New("fleet: execution slot already released")
)

// MachineDirectory participates in the caller's transaction. Callers must
// authorize the current project/assignment before reserving or releasing slots;
// machine availability is a scheduling fact and is never an IAM permission.
// Models returned here are fleet-owned facts, not another module's storage API.
type MachineDirectory struct {
	ModelFamilyOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (string, error)
	BindRunnerContext  func(context.Context, guid.ID) (context.Context, error)
	ModelPriceOn       func(context.Context, *gorm.DB, guid.ID, string, string) (*TokenPrice, error)
	ValidateSessionOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64) error
	LockOn             func(context.Context, *gorm.DB, guid.ID, guid.ID) (MachineFact, error)
	ReacquireSlotOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (SlotFact, error)
	ByIDOn             func(context.Context, *gorm.DB, guid.ID, guid.ID) (MachineFact, error)
	ResolveWorkspaceOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (WorkspaceFact, error)
	ReserveSlotOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (SlotFact, error)
	ReleaseSlotOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}
type MachineFact struct {
	ID, PrincipalID guid.ID
	Name, State, OS string
	SlotCount       int
	ConnEpoch       int64
}
type WorkspaceFact struct {
	ID            guid.ID
	Path, PathKey string
}
type SlotFact struct {
	ID, MachineID, ExecutionID guid.ID
	SlotNo                     int
}
type SessionFact struct {
	ID         guid.ID
	ConnEpoch  int64
	LastSeenAt time.Time
}

func fleetTransaction(tx *gorm.DB, tenant, id guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	return nil
}
func (m *Module) machineOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, lock bool) (*model.Machine, error) {
	if m == nil || m.names.Machines.IsZero() {
		return nil, ErrUnavailable
	}
	if err := fleetTransaction(tx, tenant, id); err != nil {
		return nil, err
	}
	q := query.BindMachines(tx, m.names)
	scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if lock {
		scope = scope.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := scope.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	return row, err
}

// BindRunnerContext derives the tenant from persisted machine ownership, never
// from the token's external organization claim or a client-supplied tenant.
// Commands still recheck current IAM and session authority in their transaction.
func (m *Module) BindRunnerContext(ctx context.Context, machine guid.ID) (context.Context, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID <= 0 || machine <= 0 {
		return ctx, ErrMachineForbidden
	}
	db, err := m.writer()
	if err != nil {
		return ctx, err
	}
	q := query.BindMachines(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(machine)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ctx, ErrMachineNotFound
	}
	if err != nil {
		return ctx, err
	}
	if row.PrincipalID != claims.PrincipalID || (claims.TenantID > 0 && claims.TenantID != row.TenantID) {
		return ctx, ErrMachineForbidden
	}
	bound := *claims
	bound.TenantID, bound.EntityID = row.TenantID, 0
	return authn.WithClaims(ctx, &bound), nil
}

func machineState(row *model.Machine, now time.Time) string {
	if (row.State == "ONLINE" || row.State == "DRAINING") && (row.LastSeenAt == nil || now.Sub(*row.LastSeenAt) > 30*time.Second) {
		return "OFFLINE"
	}
	return row.State
}
func (m *Module) MachineByIDOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (MachineFact, error) {
	row, err := m.machineOn(ctx, tx, tenant, id, false)
	if err != nil {
		return MachineFact{}, err
	}
	return MachineFact{ID: row.ID, PrincipalID: row.PrincipalID, Name: row.Name, State: machineState(row, time.Now().UTC()), OS: row.OS, SlotCount: row.SlotCount, ConnEpoch: row.ConnEpoch}, nil
}
func (m *Module) LockMachineOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (MachineFact, error) {
	if _, err := m.machineOn(ctx, tx, tenant, id, true); err != nil {
		return MachineFact{}, err
	}
	return m.MachineByIDOn(ctx, tx, tenant, id)
}
func (m *Module) ResolveWorkspaceOn(ctx context.Context, tx *gorm.DB, tenant, machine, project, repository guid.ID) (WorkspaceFact, error) {
	if project <= 0 || repository <= 0 {
		return WorkspaceFact{}, ErrInvalidArgument
	}
	if _, err := m.machineOn(ctx, tx, tenant, machine, false); err != nil {
		return WorkspaceFact{}, err
	}
	q := query.BindWorkdirs(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ProjectID.Eq(project), q.RepositoryID.Eq(repository), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return WorkspaceFact{}, ErrMachineNotFound
	}
	if err != nil {
		return WorkspaceFact{}, err
	}
	return WorkspaceFact{ID: row.ID, Path: row.Path, PathKey: row.PathKey}, nil
}

type machineActorKey struct{}

func humanMachineContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, machineActorKey{}, "HUMAN")
}

func (m *Module) machineEventOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, kind string, version int, detail map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, tenant, "fleet."+kind, "machine", machine)
	for k, v := range detail {
		e.Detail[k] = v
	}
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	actorKind, authority := "SYSTEM", "SYSTEM:fleet"
	if actor > 0 && (kind == "machine.state_changed" || kind == "machine.inventory_updated" || kind == "machine.runtimes_changed" || kind == "machine.inspection_changed" || kind == "machine.credential_changed" || kind == "machine.agent_command_completed") {
		actorKind, authority = "MACHINE", "IAM"
		if ctx.Value(machineActorKey{}) == "HUMAN" {
			actorKind, authority = "HUMAN", "IAM"
		}
	} else {
		actor = 0
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: kind, Subject: events.Subject{Kind: "machine", ID: machine}, SubjectVersion: int64(version), Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: authority}, Data: detail})
	return err
}

// OpenSessionOn requires the authenticated machine principal and validates its
// current IAM status under a principal lock before locking the machine row.
func (m *Module) OpenSessionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, incarnation string) (SessionFact, error) {
	if incarnation == "" || len(incarnation) > 64 || strings.IndexFunc(incarnation, unicode.IsControl) >= 0 {
		return SessionFact{}, ErrInvalidArgument
	}
	if err := fleetTransaction(tx, tenant, id); err != nil {
		return SessionFact{}, err
	}
	if m == nil || m.ids == nil || m.accounts == nil || m.principals.Lock == nil {
		return SessionFact{}, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return SessionFact{}, ErrMachineForbidden
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return SessionFact{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	valid, err := m.accounts(ctx, tx, tenant, actor, now)
	if err != nil {
		return SessionFact{}, err
	}
	if !valid {
		return SessionFact{}, ErrMachineForbidden
	}
	row, err := m.machineOn(ctx, tx, tenant, id, true)
	if err != nil {
		return SessionFact{}, err
	}
	if row.PrincipalID != actor {
		return SessionFact{}, ErrMachineForbidden
	}
	if row.State == "REVOKED" {
		return SessionFact{}, ErrMachineRevoked
	}
	sessions := query.BindSessions(tx, m.names)
	if row.CurrentSessionID != nil {
		old, e := sessions.WithContext(ctx).Where(sessions.TenantID.Eq(tenant), sessions.MachineID.Eq(id), sessions.ID.Eq(*row.CurrentSessionID)).First()
		if e != nil {
			return SessionFact{}, e
		}
		if old.State == "ACTIVE" && now.Sub(old.LastSeenAt) <= 30*time.Second {
			if old.IncarnationID == incarnation {
				return SessionFact{ID: old.ID, ConnEpoch: old.ConnEpoch, LastSeenAt: old.LastSeenAt}, nil
			}
			return SessionFact{}, ErrSessionActive
		}
		if old.State == "ACTIVE" {
			if _, e = sessions.WithContext(ctx).Where(sessions.ID.Eq(old.ID), sessions.TenantID.Eq(tenant)).Updates(map[string]any{"state": "SUPERSEDED", "closed_at": now}); e != nil {
				return SessionFact{}, e
			}
		}
	}
	if row.ConnEpoch == math.MaxInt64 {
		return SessionFact{}, ErrUnavailable
	}
	next, err := m.ids.Next()
	if err != nil {
		return SessionFact{}, err
	}
	if next <= 0 {
		return SessionFact{}, ErrUnavailable
	}
	row.ConnEpoch++
	session := &model.RunnerSession{ID: guid.ID(next), TenantID: tenant, MachineID: id, IncarnationID: incarnation, ConnEpoch: row.ConnEpoch, State: "ACTIVE", CreatedAt: now, LastSeenAt: now}
	if err = sessions.WithContext(ctx).Create(session); err != nil {
		return SessionFact{}, err
	}
	state := "ONLINE"
	if row.DrainRequested {
		state = "DRAINING"
	}
	q := query.BindMachines(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": state, "conn_epoch": row.ConnEpoch, "current_session_id": session.ID, "last_seen_at": now, "updated_at": now, "version": row.Version + 1}); err != nil {
		return SessionFact{}, err
	}
	if err = m.machineEventOn(ctx, tx, tenant, id, "machine.state_changed", row.Version+1, map[string]any{"state": state, "conn_epoch": row.ConnEpoch}); err != nil {
		return SessionFact{}, err
	}
	enrollments := query.BindEnrollments(tx, m.names)
	if _, err = enrollments.WithContext(ctx).Where(enrollments.TenantID.Eq(tenant), enrollments.MachineID.Eq(id), enrollments.State.Eq("COMPLETE"), enrollments.AcknowledgedAt.IsNull()).Update(enrollments.AcknowledgedAt, now); err != nil {
		return SessionFact{}, err
	}
	return SessionFact{ID: session.ID, ConnEpoch: session.ConnEpoch, LastSeenAt: now}, nil
}

// HeartbeatOn never revives an expired or superseded connection. Reconnection
// requires a new session/epoch so delayed packets cannot extend old leases.
func (m *Module) HeartbeatOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) error {
	if session <= 0 || epoch <= 0 {
		return ErrInvalidArgument
	}
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return err
	}
	if m == nil || m.accounts == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrMachineForbidden
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	valid, err := m.accounts(ctx, tx, tenant, actor, now)
	if err != nil {
		return err
	}
	if !valid {
		return ErrMachineForbidden
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return err
	}
	if row.PrincipalID != actor {
		return ErrMachineForbidden
	}
	if row.State == "REVOKED" {
		return ErrMachineRevoked
	}
	if machineState(row, now) == "OFFLINE" || row.ConnEpoch != epoch || row.CurrentSessionID == nil || *row.CurrentSessionID != session {
		return ErrSessionStale
	}
	s := query.BindSessions(tx, m.names)
	r, err := s.WithContext(ctx).Where(s.ID.Eq(session), s.TenantID.Eq(tenant), s.MachineID.Eq(machine), s.ConnEpoch.Eq(epoch), s.State.Eq("ACTIVE")).Update(s.LastSeenAt, now)
	if err != nil {
		return err
	}
	if r.RowsAffected != 1 {
		return ErrSessionStale
	}
	q := query.BindMachines(tx, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(machine), q.TenantID.Eq(tenant)).Update(q.LastSeenAt, now)
	return err
}

func (m *Module) ReserveSlotOn(ctx context.Context, tx *gorm.DB, tenant, machine, execution guid.ID) (SlotFact, error) {
	return m.reserveSlotOn(ctx, tx, tenant, machine, execution, false)
}

// ReacquireSlotOn is reserved for an explicitly authorized recovery. Ordinary
// reservation replay still rejects released ownership.
func (m *Module) ReacquireSlotOn(ctx context.Context, tx *gorm.DB, tenant, machine, execution guid.ID) (SlotFact, error) {
	return m.reserveSlotOn(ctx, tx, tenant, machine, execution, true)
}
func (m *Module) reserveSlotOn(ctx context.Context, tx *gorm.DB, tenant, machine, execution guid.ID, reacquire bool) (SlotFact, error) {
	if execution <= 0 {
		return SlotFact{}, ErrInvalidArgument
	}
	// Follow the same principal -> machine lock order as runner requests. A
	// still-fresh heartbeat cannot authorize scheduling on a disabled account.
	identity, err := m.machineOn(ctx, tx, tenant, machine, false)
	if err != nil {
		return SlotFact{}, err
	}
	if m.accounts == nil || m.principals.Lock == nil {
		return SlotFact{}, ErrUnavailable
	}
	if err = m.principals.Lock(ctx, tx, identity.PrincipalID); err != nil {
		return SlotFact{}, err
	}
	valid, err := m.accounts(ctx, tx, tenant, identity.PrincipalID, time.Now().UTC())
	if err != nil {
		return SlotFact{}, err
	}
	if !valid {
		return SlotFact{}, ErrMachineForbidden
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return SlotFact{}, err
	}
	resets := query.BindResetCommands(tx, m.names)
	pending, err := resets.WithContext(ctx).Where(resets.TenantID.Eq(tenant), resets.MachineID.Eq(machine), resets.State.In("QUEUED", "RUNNING")).Count()
	if err != nil {
		return SlotFact{}, err
	}
	if pending != 0 {
		return SlotFact{}, ErrVersionConflict
	}
	q := query.BindSlots(tx, m.names)
	previous, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution)).First()
	if err == nil {
		if previous.MachineID != machine {
			return SlotFact{}, ErrInvalidArgument
		}
		if previous.ReleasedAt != nil && !reacquire {
			return SlotFact{}, ErrSlotReleased
		}
		if previous.ReleasedAt == nil {
			return SlotFact{ID: previous.ID, MachineID: machine, ExecutionID: execution, SlotNo: previous.SlotNo}, nil
		}
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return SlotFact{}, err
	}
	switch machineState(row, time.Now().UTC()) {
	case "REVOKED":
		return SlotFact{}, ErrMachineRevoked
	case "OFFLINE":
		return SlotFact{}, ErrMachineOffline
	case "DRAINING":
		return SlotFact{}, ErrMachineDraining
	}
	if row.DrainRequested {
		return SlotFact{}, ErrMachineDraining
	}
	if m.ids == nil {
		return SlotFact{}, ErrUnavailable
	}
	used, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ReleasedAt.IsNull()).Find()
	if err != nil {
		return SlotFact{}, err
	}
	slot := 0
	for i := 1; i <= row.SlotCount; i++ {
		free := true
		for _, r := range used {
			if r.SlotNo == i {
				free = false
				break
			}
		}
		if free {
			slot = i
			break
		}
	}
	if slot == 0 {
		return SlotFact{}, ErrSlotsFull
	}
	if previous != nil && previous.ReleasedAt != nil {
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(previous.ID)).Updates(map[string]any{"slot_no": slot, "active_slot": slot, "released_at": nil}); err != nil {
			return SlotFact{}, err
		}
		if err = m.machineEventOn(ctx, tx, tenant, machine, "machine.slot_reserved", row.Version, map[string]any{"execution_id": execution.String(), "slot_no": slot, "recovery": true}); err != nil {
			return SlotFact{}, err
		}
		return SlotFact{ID: previous.ID, MachineID: machine, ExecutionID: execution, SlotNo: slot}, nil
	}
	id, err := m.ids.Next()
	if err != nil {
		return SlotFact{}, err
	}
	if id <= 0 {
		return SlotFact{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	reservation := &model.SlotReservation{ID: guid.ID(id), TenantID: tenant, MachineID: machine, ExecutionID: execution, SlotNo: slot, ActiveSlot: &slot, CreatedAt: now}
	if err = q.WithContext(ctx).Create(reservation); err != nil {
		return SlotFact{}, err
	}
	if err = m.machineEventOn(ctx, tx, tenant, machine, "machine.slot_reserved", row.Version, map[string]any{"execution_id": execution.String(), "slot_no": slot}); err != nil {
		return SlotFact{}, err
	}
	return SlotFact{ID: reservation.ID, MachineID: machine, ExecutionID: execution, SlotNo: slot}, nil
}
func (m *Module) ReleaseSlotOn(ctx context.Context, tx *gorm.DB, tenant, machine, execution guid.ID) error {
	if execution <= 0 {
		return ErrInvalidArgument
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return err
	}
	q := query.BindSlots(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ExecutionID.Eq(execution), q.ReleasedAt.IsNull()).Updates(map[string]any{"released_at": time.Now().UTC().Truncate(time.Millisecond), "active_slot": nil})
	if err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return m.machineEventOn(ctx, tx, tenant, machine, "machine.slot_released", row.Version, map[string]any{"execution_id": execution.String()})
}
