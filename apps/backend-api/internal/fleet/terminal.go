package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
)

var ErrTerminalAccessDisabled = errors.New("fleet: remote terminal disabled")
var ErrTerminalClosed = errors.New("fleet: terminal closed")

type terminalRecordingSealKey struct{}

type TerminalExecution struct {
	ID, ProjectID, MachineID guid.ID
	Workspace                string
	DispatchEpoch            int64
}
type TerminalExecutionResolver func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error)
type TerminalExecutionConnector struct {
	Bind func(TerminalExecutionResolver) error
}
type TerminalTransport struct {
	Available func(guid.ID, guid.ID) bool
	Open      func(context.Context, TerminalSession, int, int) (bool, error)
	Close     func(context.Context, TerminalSession, string) error
}
type terminalServices struct {
	permission core.AuthorizationExplanation
	execution  TerminalExecutionResolver
	recordings *files.TerminalRecordings
	transport  TerminalTransport
	mu         sync.Mutex
}
type TerminalSession struct {
	TunnelMaxStreams int        `json:"-"`
	ID               guid.ID    `json:"id"`
	TenantID         guid.ID    `json:"-"`
	MachineID        guid.ID    `json:"machine_id"`
	ExecutionID      guid.ID    `json:"execution_id"`
	ProjectID        guid.ID    `json:"-"`
	OpenedBy         guid.ID    `json:"opened_by"`
	RunnerSessionID  guid.ID    `json:"-"`
	ConnEpoch        int64      `json:"-"`
	DispatchEpoch    int64      `json:"-"`
	Workspace        string     `json:"-"`
	State            string     `json:"state" enum:"OPENING,OPEN,CLOSED"`
	Sandboxed        bool       `json:"sandboxed"`
	OpenedAt         time.Time  `json:"opened_at"`
	LastInputAt      time.Time  `json:"last_input_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CloseReason      string     `json:"close_reason,omitempty"`
	RecordingFileID  *guid.ID   `json:"-"`
	RecordingSHA256  string     `json:"-"`
	ChannelURL       string     `json:"channel_url"`
}

func terminalView(r *model.TerminalSession) TerminalSession {
	return TerminalSession{ID: r.ID, TenantID: r.TenantID, MachineID: r.MachineID, ExecutionID: r.ExecutionID, ProjectID: r.ProjectID, OpenedBy: r.OpenedBy, RunnerSessionID: r.RunnerSessionID, ConnEpoch: r.ConnEpoch, DispatchEpoch: r.DispatchEpoch, Workspace: r.Workspace, State: r.State, Sandboxed: r.Sandboxed, OpenedAt: r.OpenedAt, LastInputAt: r.LastInputAt, ExpiresAt: r.ExpiresAt, ClosedAt: r.ClosedAt, CloseReason: r.CloseReason, RecordingFileID: r.RecordingFileID, RecordingSHA256: r.RecordingSHA256, ChannelURL: "/terminal-sessions/" + r.ID.String() + "/stream"}
}
func (m *Module) provideTerminal(s *module.Services) error {
	return module.Provide[TerminalExecutionConnector](s, TerminalExecutionConnector{Bind: func(f TerminalExecutionResolver) error {
		if f == nil || m.terminal.execution != nil {
			return ErrInvalidArgument
		}
		m.terminal.execution = f
		return nil
	}})
}
func (m *Module) injectTerminal(s *module.Services) error {
	var err error
	m.terminal.permission, err = module.Resolve[core.AuthorizationExplanation](s)
	if err != nil {
		return err
	}
	m.terminal.recordings, err = module.Resolve[*files.TerminalRecordings](s)
	if err != nil {
		return err
	}
	return m.terminal.recordings.Bind(m.authorizeTerminalRecordingOn)
}
func (m *Module) BindTerminalTransport(t TerminalTransport) error {
	if t.Available == nil || t.Open == nil || t.Close == nil || m.terminal.transport.Open != nil {
		return ErrInvalidArgument
	}
	m.terminal.transport = t
	return nil
}
func (m *Module) terminalOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, lock bool) (*model.TerminalSession, error) {
	if err := fleetTransaction(tx, tenant, id); err != nil {
		return nil, err
	}
	q := query.BindTerminalSessions(tx, m.names)
	s := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if lock {
		s = s.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	r, err := s.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	return r, err
}
func (m *Module) terminalEventOn(ctx context.Context, tx *gorm.DB, r *model.TerminalSession, kind string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, r.TenantID, kind, "terminal_session", r.ID)
	e.Detail["execution_id"] = r.ExecutionID.String()
	e.Detail["machine_id"] = r.MachineID.String()
	e.Detail["sandboxed"] = r.Sandboxed
	e.Detail["reason"] = r.CloseReason
	if kind == "execution.terminal_closed" {
		e.Detail["recording_sha256"] = r.RecordingSHA256
		e.Detail["recording_file_id"] = r.RecordingFileID
	}
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: "terminal.changed", Subject: events.Subject{Kind: "terminal_session", ID: r.ID}, Actor: events.Actor{Kind: "HUMAN", ID: r.OpenedBy, AuthoritySource: "IAM"}, Data: map[string]any{"execution_id": r.ExecutionID, "state": r.State, "reason": r.CloseReason}})
	return err
}
func (m *Module) terminalAuthorityOn(ctx context.Context, tx *gorm.DB, r *model.TerminalSession, live bool) error {
	if m.terminal.execution == nil {
		return ErrUnavailable
	}
	if authn.PrincipalIDFromContext(ctx) != r.OpenedBy || authn.TenantIDFromContext(ctx) != r.TenantID {
		return ErrMachineForbidden
	}
	permission := "terminal_interact"
	if !live {
		permission = "terminal_metadata"
	}
	f, err := m.terminal.execution(ctx, tx, r.TenantID, r.ExecutionID, permission)
	if err != nil {
		return err
	}
	if f.MachineID != r.MachineID || f.ProjectID != r.ProjectID || f.Workspace != r.Workspace || f.DispatchEpoch != r.DispatchEpoch {
		return ErrSessionStale
	}
	if !live {
		return nil
	}
	if r.State == "CLOSED" {
		return ErrTerminalClosed
	}
	now := time.Now().UTC()
	if !now.Before(r.ExpiresAt) || now.Sub(r.LastInputAt) >= 15*time.Minute {
		return ErrTerminalClosed
	}
	access := query.BindTerminalAccess(tx, m.names)
	a, err := access.WithContext(ctx).Where(access.TenantID.Eq(r.TenantID), access.MachineID.Eq(r.MachineID)).First()
	if err != nil {
		return err
	}
	if !a.Enabled {
		return ErrTerminalAccessDisabled
	}
	machine, err := m.machineOn(ctx, tx, r.TenantID, r.MachineID, true)
	if err != nil {
		return err
	}
	if machine.CurrentSessionID == nil || *machine.CurrentSessionID != r.RunnerSessionID || machine.ConnEpoch != r.ConnEpoch {
		return ErrSessionStale
	}
	if machineState(machine, now) != "ONLINE" && machineState(machine, now) != "DRAINING" {
		return ErrMachineOffline
	}
	return nil
}
func (m *Module) OpenTerminal(ctx context.Context, tenant, machine, execution guid.ID, cookie, proof string, cols, rows int) (TerminalSession, error) {
	var out TerminalSession
	if tenant <= 0 || machine <= 0 || execution <= 0 || cols < 1 || cols > 500 || rows < 1 || rows > 200 {
		return out, ErrInvalidArgument
	}
	if m == nil || m.ids == nil || m.revokeProof == nil || m.terminal.execution == nil || !m.terminal.recordings.Available() || m.terminal.transport.Available == nil || !m.terminal.transport.Available(tenant, machine) {
		return out, ErrUnavailable
	}
	if cookie == "" || proof == "" {
		return out, ErrMachineForbidden
	}
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	var row *model.TerminalSession
	maxStreams := 256
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		row, maxStreams, e = m.reserveTerminalOn(ctx, tx, tenant, machine, execution, cookie, proof)
		return e
	})
	if err != nil {
		return out, err
	}
	return m.activateTerminal(ctx, row, maxStreams, cols, rows)
}
func (m *Module) reserveTerminalOn(ctx context.Context, tx *gorm.DB, tenant, machine, execution guid.ID, cookie, proof string) (*model.TerminalSession, int, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, 0, ErrMachineForbidden
	}
	if m.principals.Lock == nil {
		return nil, 0, ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return nil, 0, e
	}
	if e := m.revokeProof(ctx, tx, actor, cookie, proof, "terminal_interactive", "execution:"+execution.String()); e != nil {
		return nil, 0, e
	}
	f, e := m.terminal.execution(ctx, tx, tenant, execution, "terminal_interact")
	if e != nil {
		return nil, 0, e
	}
	if f.MachineID != machine {
		return nil, 0, ErrMachineForbidden
	}
	r, e := m.machineOn(ctx, tx, tenant, machine, true)
	if e != nil {
		return nil, 0, e
	}
	if machineState(r, time.Now().UTC()) != "ONLINE" && machineState(r, time.Now().UTC()) != "DRAINING" {
		return nil, 0, ErrMachineOffline
	}
	if r.CurrentSessionID == nil {
		return nil, 0, ErrMachineOffline
	}
	q := query.BindTerminalAccess(tx, m.names)
	a, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, 0, ErrTerminalAccessDisabled
	}
	if e != nil {
		return nil, 0, e
	}
	maxStreams := a.TunnelMaxStreams
	if maxStreams == 0 {
		maxStreams = 256
	}
	if !a.Enabled {
		return nil, 0, ErrTerminalAccessDisabled
	}
	sessions := query.BindTerminalSessions(tx, m.names)
	n, e := sessions.WithContext(ctx).Where(sessions.TenantID.Eq(tenant), sessions.ActiveExecution.Eq(execution)).Count()
	if e != nil {
		return nil, 0, e
	}
	if n > 0 {
		return nil, 0, ErrSessionActive
	}
	id, e := m.ids.Next()
	if e != nil {
		return nil, 0, e
	}
	if id <= 0 {
		return nil, 0, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &model.TerminalSession{ID: guid.ID(id), TenantID: tenant, MachineID: machine, ExecutionID: execution, ProjectID: f.ProjectID, OpenedBy: authn.PrincipalIDFromContext(ctx), RunnerSessionID: *r.CurrentSessionID, ConnEpoch: r.ConnEpoch, DispatchEpoch: f.DispatchEpoch, ActiveExecution: &execution, State: "OPENING", Workspace: f.Workspace, OpenedAt: now, LastInputAt: now, ExpiresAt: now.Add(2 * time.Hour), RecordingChunks: "[]"}
	if err := sessions.WithContext(ctx).Create(row); err != nil {
		return nil, 0, err
	}
	return row, maxStreams, nil
}
func (m *Module) activateTerminal(ctx context.Context, row *model.TerminalSession, maxStreams, cols, rows int) (TerminalSession, error) {
	tenant := row.TenantID
	db, err := m.writer()
	if err != nil {
		return TerminalSession{}, err
	}
	var out TerminalSession
	out = terminalView(row)
	out.TunnelMaxStreams = maxStreams
	header, _ := json.Marshal(map[string]any{"version": 2, "width": cols, "height": rows, "timestamp": row.OpenedAt.Unix(), "env": map[string]string{"TERM": "xterm-256color"}})
	m.terminal.mu.Lock()
	err = m.recordTerminalBytes(ctx, row, append(header, '\n'), false)
	m.terminal.mu.Unlock()
	if err != nil {
		_, _ = m.CloseTerminal(ctx, tenant, row.ID, "RECORDING_FAILED")
		return out, err
	}
	sandboxed, err := m.terminal.transport.Open(ctx, out, cols, rows)
	if err != nil {
		_, _ = m.CloseTerminal(ctx, tenant, row.ID, "OPEN_FAILED")
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, row.ID, true)
		if e != nil {
			return e
		}
		if e = m.terminalAuthorityOn(ctx, tx, r, true); e != nil {
			return e
		}
		q := query.BindTerminalSessions(tx, m.names)
		_, e = q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": "OPEN", "sandboxed": sandboxed})
		if e != nil {
			return e
		}
		r.State = "OPEN"
		r.Sandboxed = sandboxed
		out = terminalView(r)
		return m.terminalEventOn(ctx, tx, r, "execution.terminal_opened")
	})
	if err != nil {
		_, _ = m.CloseTerminal(ctx, tenant, row.ID, "OPEN_FAILED")
	}
	return out, err
}
func (m *Module) GetTerminal(ctx context.Context, tenant, id guid.ID) (TerminalSession, error) {
	var out TerminalSession
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if e = m.terminalAuthorityOn(ctx, tx, r, false); e != nil {
			return e
		}
		out = terminalView(r)
		return nil
	})
	return out, err
}
func (m *Module) AuthorizeTerminalIO(ctx context.Context, tenant, id guid.ID, input bool) error {
	db, err := m.writer()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if e = m.terminalAuthorityOn(ctx, tx, r, true); e != nil {
			return e
		}
		if input && r.State != "OPEN" {
			return ErrTerminalClosed
		}
		return nil
	})
}
func (m *Module) ValidateTerminalRunner(ctx context.Context, tenant, id, machine, session guid.ID, epoch int64) (TerminalSession, error) {
	var out TerminalSession
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if r.MachineID != machine || r.RunnerSessionID != session || r.ConnEpoch != epoch {
			return ErrSessionStale
		}
		if e = m.terminalAuthorityOn(terminalOwnerContext(ctx, tenant, r.OpenedBy), tx, r, true); e != nil {
			return e
		}
		if e = m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); e != nil {
			return e
		}
		out = terminalView(r)
		return nil
	})
	return out, err
}
func (m *Module) RecordTerminal(ctx context.Context, tenant, id guid.ID, direction string, data []byte) error {
	if direction != "i" && direction != "o" || len(data) == 0 || len(data) > 256<<10 || !utf8.Valid(data) {
		return ErrInvalidArgument
	}
	m.terminal.mu.Lock()
	defer m.terminal.mu.Unlock()
	db, err := m.writer()
	if err != nil {
		return err
	}
	var row *model.TerminalSession
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		if authn.TenantIDFromContext(ctx) != tenant {
			return ErrMachineForbidden
		}
		runner := authn.PrincipalIDFromContext(ctx) != r.OpenedBy
		if runner {
			machine, e := m.machineOn(ctx, tx, tenant, r.MachineID, false)
			if e != nil {
				return e
			}
			if authn.PrincipalIDFromContext(ctx) != machine.PrincipalID || direction != "o" {
				return ErrMachineForbidden
			}
		}
		if e = m.terminalAuthorityOn(terminalOwnerContext(ctx, tenant, r.OpenedBy), tx, r, true); e != nil {
			return e
		}
		if runner {
			if e = m.HeartbeatOn(ctx, tx, tenant, r.MachineID, r.RunnerSessionID, r.ConnEpoch); e != nil {
				return e
			}
		}
		row = r
		return nil
	})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal([]any{time.Since(row.OpenedAt).Seconds(), direction, string(data)})
	if err != nil {
		return err
	}
	return m.recordTerminalBytes(ctx, row, append(encoded, '\n'), direction == "i")
}
func (m *Module) recordTerminalBytes(ctx context.Context, r *model.TerminalSession, data []byte, input bool) error {
	db, err := m.writer()
	if err != nil {
		return err
	}
	var observed *model.TerminalSession
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		observed, e = m.terminalOn(ctx, tx, r.TenantID, r.ID, false)
		return e
	})
	if err != nil {
		return err
	}
	if observed.State == "CLOSED" {
		return ErrTerminalClosed
	}
	var chunks []guid.ID
	if err = json.Unmarshal([]byte(observed.RecordingChunks), &chunks); err != nil {
		return err
	}
	var tail guid.ID
	if len(chunks) > 0 {
		tail = chunks[len(chunks)-1]
	}
	// Storage reads/uploads must not hold a transaction or a session row lock.
	staged, replaceTail, err := m.terminal.recordings.StageAppendChunks(ctx, r.OpenedBy, tail, data)
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, e := m.terminalOn(ctx, tx, r.TenantID, r.ID, true)
		if e != nil {
			return e
		}
		if current.State == "CLOSED" {
			return ErrTerminalClosed
		}
		if current.RecordingChunks != observed.RecordingChunks {
			return ErrVersionConflict
		}
		if input && current.State != "OPEN" {
			return ErrTerminalClosed
		}
		if replaceTail {
			chunks = chunks[:len(chunks)-1]
		}
		for _, chunk := range staged {
			id, e := m.terminal.recordings.AttachChunkOn(ctx, tx, chunk, r.OpenedBy)
			if e != nil {
				return e
			}
			chunks = append(chunks, id)
		}
		raw, e := json.Marshal(chunks)
		if e != nil {
			return e
		}
		q := query.BindTerminalSessions(tx, m.names)
		updates := map[string]any{"recording_chunks": string(raw)}
		if input {
			updates["last_input_at"] = time.Now().UTC().Truncate(time.Millisecond)
		}
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).Updates(updates)
		if e != nil {
			return e
		}
		if replaceTail {
			return m.terminal.recordings.ReleaseChunksOn(ctx, tx, r.OpenedBy, []guid.ID{tail})
		}
		return nil
	})
}
func (m *Module) CloseTerminal(ctx context.Context, tenant, id guid.ID, reason string) (TerminalSession, error) {
	m.terminal.mu.Lock()
	defer m.terminal.mu.Unlock()
	var out TerminalSession
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	var row *model.TerminalSession
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(ctx, tx, tenant, id, false)
		if e != nil {
			return e
		}
		actor := authn.PrincipalIDFromContext(ctx)
		if actor != r.OpenedBy && actor != 0 {
			machine, e := m.machineOn(ctx, tx, tenant, r.MachineID, false)
			if e != nil {
				return e
			}
			if machine.PrincipalID != actor {
				return ErrMachineForbidden
			}
		}
		row = r
		return nil
	})
	if err != nil {
		return out, err
	}
	if row.State == "CLOSED" {
		return terminalView(row), nil
	}
	if reason == "" {
		reason = "USER"
	}
	if len(reason) > 64 {
		return out, ErrInvalidArgument
	}
	if m.terminal.transport.Close != nil {
		if err = m.terminal.transport.Close(ctx, terminalView(row), reason); err != nil {
			return out, err
		}
	}
	var chunks []guid.ID
	if err = json.Unmarshal([]byte(row.RecordingChunks), &chunks); err != nil {
		return out, err
	}
	var prepared *files.PreparedExecutionArtifact
	if len(chunks) > 0 {
		prepared, err = m.terminal.recordings.Seal(ctx, tenant, row.ProjectID, id, row.OpenedBy, chunks)
		if err != nil {
			return out, err
		}
	}
	actorCtx := context.WithValue(terminalOwnerContext(ctx, tenant, row.OpenedBy), terminalRecordingSealKey{}, id)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := m.terminalOn(actorCtx, tx, tenant, id, true)
		if e != nil {
			return e
		}
		if r.State == "CLOSED" {
			out = terminalView(r)
			return nil
		}
		if prepared != nil {
			pin, e := m.terminal.recordings.AttachSealedOn(actorCtx, tx, prepared, id)
			if e != nil {
				return e
			}
			r.RecordingFileID = &pin.FileID
			r.RecordingSHA256 = pin.SHA256
			if e = m.terminal.recordings.ReleaseChunksOn(actorCtx, tx, r.OpenedBy, chunks); e != nil {
				return e
			}
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		r.State = "CLOSED"
		r.CloseReason = reason
		r.ClosedAt = &now
		q := query.BindTerminalSessions(tx, m.names)
		_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": "CLOSED", "active_execution": nil, "closed_at": now, "close_reason": reason, "recording_file_id": r.RecordingFileID, "recording_sha256": r.RecordingSHA256})
		if e != nil {
			return e
		}
		out = terminalView(r)
		return m.terminalEventOn(actorCtx, tx, r, "execution.terminal_closed")
	})
	return out, err
}
func (m *Module) authorizeTerminalRecordingOn(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	tenant := authn.TenantIDFromContext(ctx)
	r, err := m.terminalOn(ctx, tx, tenant, id, false)
	if err != nil {
		return err
	}
	if owner.Scope != "entity" || owner.ID != r.ProjectID {
		return ErrMachineForbidden
	}
	// Lifecycle sealing uses the original Human; ordinary file downloads require audit_read.
	if ctx.Value(terminalRecordingSealKey{}) == id && authn.PrincipalIDFromContext(ctx) == r.OpenedBy && r.State != "CLOSED" {
		return nil
	}
	if m.terminal.execution == nil {
		return ErrUnavailable
	}
	_, err = m.terminal.execution(ctx, tx, tenant, r.ExecutionID, "audit_read")
	return err
}
func (m *Module) registerTerminalActions() error {
	return m.registrar.Registry().Contribute([]authz.Action{{Resource: "machine_access", Verb: "manage", Scope: authz.ActionScopeTenant, Summary: "manage remote machine access"}}, nil)
}

// The existing tenant watchdog also recovers metadata left by a process restart.
// It never replays a PTY or resumes a lost stream.
func (m *Module) expireTerminalSessions(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindTerminalSessions(db, m.names)
	cursor := guid.ID(0)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(cursor), q.State.Neq("CLOSED")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, r := range rows {
			cursor = r.ID
			reason := ""
			if !now.Before(r.ExpiresAt) {
				reason = "MAX_DURATION"
			} else if now.Sub(r.LastInputAt) >= 15*time.Minute {
				reason = "IDLE"
			} else if now.Sub(r.OpenedAt) > 30*time.Second {
				if m.terminalBroker == nil {
					reason = "SERVER_RESTART"
				} else if _, e := m.terminalBroker.lookup(r.ID); e != nil {
					reason = "CONNECTION_LOST"
				}
			}
			if reason != "" {
				if _, err = m.CloseTerminal(terminalOwnerContext(ctx, tenant, r.OpenedBy), tenant, r.ID, reason); err != nil {
					return err
				}
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

// Lifecycle and runner checks retain the original owner, then resolve current IAM.
func terminalOwnerContext(ctx context.Context, tenant, actor guid.ID) context.Context {
	return authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor, SubjectType: authn.SubjectTypePrincipal})
}
