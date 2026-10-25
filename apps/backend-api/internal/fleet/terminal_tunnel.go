package fleet

import (
	"context"
	"errors"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
)

// The broker never persists tunnel bytes. A process restart or outbound stream
// failure resets its sessions; commands are never replayed to a new connection.
const terminalFrameLimit = 16 * 1024

type terminalFrame struct {
	Kind          string `json:"kind"`
	SessionID     string `json:"session_id"`
	Workspace     string `json:"workspace,omitempty"`
	ExecutionID   string `json:"execution_id,omitempty"`
	DispatchEpoch int64  `json:"dispatch_epoch,omitempty"`
	Columns       int    `json:"columns,omitempty"`
	Rows          int    `json:"rows,omitempty"`
	Data          []byte `json:"data,omitempty"`
	Seq           int64  `json:"seq"`
	Sandboxed     bool   `json:"sandboxed"`
	Reason        string `json:"reason,omitempty"`
}

type terminalConnection struct {
	tenant, machine, session guid.ID
	epoch                    int64
	frames                   chan terminalFrame
	done                     chan struct{}
	once                     sync.Once
	closed                   map[guid.ID]bool
	closedOrder              []guid.ID
	pending                  chan struct{}
}
type terminalStream struct {
	mu                  sync.Mutex
	session             TerminalSession
	human               context.Context
	connection          *terminalConnection
	opened              chan terminalFrame
	output              chan terminalFrame
	done                chan struct{}
	inputSeq, outputSeq int64
	watching            bool
	bufferMu            sync.Mutex
	outputClosed        bool
	release             func()
}
type terminalBroker struct {
	mu          sync.Mutex
	connections map[[2]guid.ID]*terminalConnection
	streams     map[guid.ID]*terminalStream
	budgets     map[[2]guid.ID]*tunnelBandwidth
}

func newTerminalBroker() *terminalBroker {
	return &terminalBroker{connections: make(map[[2]guid.ID]*terminalConnection), streams: make(map[guid.ID]*terminalStream), budgets: make(map[[2]guid.ID]*tunnelBandwidth)}
}
func (b *terminalBroker) available(tenant, machine guid.ID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.connections[[2]guid.ID{tenant, machine}]
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}
func (b *terminalBroker) connect(tenant, machine, session guid.ID, epoch int64) (*terminalConnection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := [2]guid.ID{tenant, machine}
	if b.connections[key] != nil {
		return nil, ErrSessionActive
	}
	c := &terminalConnection{tenant: tenant, machine: machine, session: session, epoch: epoch, frames: make(chan terminalFrame, 256), done: make(chan struct{}), closed: make(map[guid.ID]bool), pending: make(chan struct{}, 256)}
	b.connections[key] = c
	return c, nil
}
func (b *terminalBroker) lookup(id guid.ID) (*terminalStream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.streams[id]
	if s == nil {
		return nil, ErrMachineNotFound
	}
	return s, nil
}
func (b *terminalBroker) open(ctx context.Context, session TerminalSession, columns, rows int, releases ...func()) (bool, error) {
	b.mu.Lock()
	c := b.connections[[2]guid.ID{session.TenantID, session.MachineID}]
	if c == nil || c.session != session.RunnerSessionID || c.epoch != session.ConnEpoch {
		b.mu.Unlock()
		return false, ErrSessionStale
	}
	count := 0
	for _, s := range b.streams {
		if s.connection == c {
			count++
		}
		if s.session.TenantID == session.TenantID && s.session.ExecutionID == session.ExecutionID {
			b.mu.Unlock()
			return false, ErrSessionActive
		}
	}
	limit := session.TunnelMaxStreams
	if limit <= 0 {
		limit = 256
	}
	if count >= limit {
		b.mu.Unlock()
		return false, ErrSessionActive
	}
	s := &terminalStream{session: session, human: context.WithoutCancel(ctx), connection: c, opened: make(chan terminalFrame, 1), output: make(chan terminalFrame, 16), done: make(chan struct{})}
	if len(releases) > 0 {
		s.release = releases[0]
	}
	b.streams[session.ID] = s
	b.mu.Unlock()
	if err := sendTerminalFrame(ctx, c, terminalFrame{Kind: "open", SessionID: session.ID.String(), Workspace: session.Workspace, ExecutionID: session.ExecutionID.String(), DispatchEpoch: session.DispatchEpoch, Columns: columns, Rows: rows}); err != nil {
		_ = b.close(session, "CONNECTION_LOST")
		return false, err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case f := <-s.opened:
		if f.Kind != "opened" {
			_ = b.close(session, "OPEN_REJECTED")
			return false, ErrUnavailable
		}
		return f.Sandboxed, nil
	case <-ctx.Done():
		_ = b.close(session, "OPEN_CANCELED")
		return false, ctx.Err()
	case <-c.done:
		_ = b.close(session, "CONNECTION_LOST")
		return false, ErrUnavailable
	case <-timer.C:
		_ = b.close(session, "OPEN_TIMEOUT")
		return false, ErrUnavailable
	}
}
func sendTerminalFrame(ctx context.Context, c *terminalConnection, f terminalFrame) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrUnavailable
	case c.frames <- f:
		return nil
	default:
		return ErrSessionActive
	}
}
func (b *terminalBroker) close(session TerminalSession, reason string) error {
	b.mu.Lock()
	s := b.streams[session.ID]
	if s != nil {
		delete(b.streams, session.ID)
		close(s.done)
		// A revoked shell can still have final output/exit POSTs in flight.
		// Bounded connection-local tombstones let these be discarded without
		// disconnecting other authorized terminals on the same machine.
		c := s.connection
		c.closed[session.ID] = true
		c.closedOrder = append(c.closedOrder, session.ID)
		if len(c.closedOrder) > 256 {
			delete(c.closed, c.closedOrder[0])
			c.closedOrder = c.closedOrder[1:]
		}
	}
	b.mu.Unlock()
	if s != nil {
		s.closeOutput(false)
		if s.release != nil {
			s.release()
		}
		if err := sendTerminalFrame(context.Background(), s.connection, terminalFrame{Kind: "close", SessionID: session.ID.String(), Reason: reason}); err != nil {
			// Revoke must stop the native process even under backpressure.
			s.connection.once.Do(func() { close(s.connection.done) })
		}
	}
	return nil
}

// bufferMu is independent of the receipt sequencing lock: closing a session
// while processing its final receipt must never reacquire s.mu.
func (s *terminalStream) queueOutput(f terminalFrame) error {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	if s.outputClosed {
		return ErrUnavailable
	}
	select {
	case <-s.done:
		return ErrUnavailable
	default:
	}
	select {
	case s.connection.pending <- struct{}{}:
	default:
		return errors.New("terminal connection exceeded bounded window")
	}
	select {
	case s.output <- f:
		return nil
	default:
		<-s.connection.pending
		return errors.New("terminal consumer exceeded bounded window")
	}
}
func (s *terminalStream) releaseOutput() { <-s.connection.pending }
func (s *terminalStream) closeOutput(discard bool) {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	s.outputClosed = true
	if !discard && s.watching {
		return
	}
	for {
		select {
		case <-s.output:
			s.releaseOutput()
		default:
			return
		}
	}
}
func (b *terminalBroker) discardClosedFrame(c *terminalConnection, f terminalFrame) bool {
	if (f.Kind != "output" && f.Kind != "closed" && f.Kind != "opened" && f.Kind != "rejected") || len(f.Data) > terminalFrameLimit {
		return false
	}
	id, err := guid.Parse(f.SessionID)
	if err != nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return c.closed[id]
}
func (b *terminalBroker) disconnect(c *terminalConnection) []*terminalStream {
	c.once.Do(func() { close(c.done) })
	b.mu.Lock()
	defer b.mu.Unlock()
	key := [2]guid.ID{c.tenant, c.machine}
	if b.connections[key] == c {
		delete(b.connections, key)
	}
	var result []*terminalStream
	for _, s := range b.streams {
		if s.connection == c {
			result = append(result, s)
		}
	}
	return result
}
func (m *Module) terminalTunnelInput(ctx context.Context, tenant, id guid.ID, seq int64, data []byte) error {
	if len(data) == 0 || len(data) > terminalFrameLimit || seq < 1 {
		return ErrInvalidArgument
	}
	if err := m.AuthorizeTerminalIO(ctx, tenant, id, true); err != nil {
		return err
	}
	s, err := m.terminalBroker.lookup(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq != s.inputSeq+1 {
		return ErrVersionConflict
	}
	// Record before forwarding: a recording failure must never leave unaudited
	// shell input executing on the machine.
	if err = m.RecordTerminal(ctx, tenant, id, "i", data); err != nil {
		return err
	}
	if err = sendTerminalFrame(ctx, s.connection, terminalFrame{Kind: "input", SessionID: id.String(), Seq: seq, Data: data}); err != nil {
		return err
	}
	s.inputSeq = seq
	return nil
}
func (m *Module) terminalTunnelFrame(ctx context.Context, c *terminalConnection, f terminalFrame) error {
	if f.Kind == "pong" {
		return nil
	}
	id, err := guid.Parse(f.SessionID)
	if err != nil {
		return ErrInvalidArgument
	}
	s, err := m.terminalBroker.lookup(id)
	if err != nil {
		return err
	}
	if s.connection != c {
		return ErrMachineForbidden
	}
	if _, err = m.ValidateTerminalRunner(ctx, c.tenant, id, c.machine, c.session, c.epoch); err != nil {
		return err
	}
	if len(f.Data) > terminalFrameLimit {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch f.Kind {
	case "opened", "rejected":
		if f.Seq != 1 || s.outputSeq != 0 {
			return ErrVersionConflict
		}
		s.outputSeq = 1
		select {
		case s.opened <- f:
			return nil
		default:
			return ErrVersionConflict
		}
	case "output":
		if len(f.Data) == 0 || f.Seq != s.outputSeq+1 {
			return ErrVersionConflict
		}
		if err = m.AuthorizeTerminalIO(s.human, c.tenant, id, false); err != nil {
			return err
		}
		if err = m.RecordTerminal(s.human, c.tenant, id, "o", f.Data); err != nil {
			return err
		}
		if err = s.queueOutput(f); err != nil {
			return err
		}
		s.outputSeq = f.Seq
		return nil
	case "closed":
		if f.Seq != s.outputSeq+1 {
			return ErrVersionConflict
		}
		// The runner serializes its final output before this frame.
		_, err = m.CloseTerminal(s.human, c.tenant, id, "PROCESS_EXIT")
		return err
	default:
		return ErrInvalidArgument
	}
}

func (m *Module) terminalTunnelSweep(c *terminalConnection) error {
	b := m.terminalBroker
	b.mu.Lock()
	var all []*terminalStream
	for _, s := range b.streams {
		if s.connection == c {
			all = append(all, s)
		}
	}
	b.mu.Unlock()
	for _, s := range all {
		if err := m.AuthorizeTerminalIO(s.human, c.tenant, s.session.ID, false); err != nil {
			if _, closeErr := m.CloseTerminal(s.human, c.tenant, s.session.ID, "AUTHORITY_OR_LIFECYCLE_ENDED"); closeErr != nil {
				_ = b.close(s.session, "RECORDING_FAILURE")
				return closeErr
			}
		}
	}
	return nil
}

// Policy revocation tears down shells immediately after the policy transaction
// commits; it does not wait for the periodic connection authority sweep.
func (m *Module) closeMachineTerminals(ctx context.Context, tenant, machine guid.ID, reason string) error {
	if m.terminalBroker == nil {
		return nil
	}
	b := m.terminalBroker
	b.mu.Lock()
	var sessions []TerminalSession
	for _, s := range b.streams {
		if s.session.TenantID == tenant && s.session.MachineID == machine {
			sessions = append(sessions, s.session)
		}
	}
	b.mu.Unlock()
	var result error
	for _, s := range sessions {
		if _, err := m.CloseTerminal(terminalOwnerContext(ctx, tenant, s.OpenedBy), tenant, s.ID, reason); err != nil {
			_ = b.close(s, reason)
			result = errors.Join(result, err)
		}
	}
	return result
}
