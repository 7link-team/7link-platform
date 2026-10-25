package fleet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
)

type previewFrame struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	ExecutionID   string `json:"execution_id,omitempty"`
	DispatchEpoch int64  `json:"dispatch_epoch,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Port          int    `json:"port,omitempty"`
	Data          []byte `json:"data,omitempty"`
	Ports         []int  `json:"ports,omitempty"`
}
type previewConnection struct {
	tenant, machine, session guid.ID
	epoch                    int64
	frames                   chan previewFrame
	done                     chan struct{}
	once                     sync.Once
	streams                  map[string]*previewStream
	closed                   map[string]bool
	closedOrder              []string
	pending                  chan struct{}
}
type previewBroker struct {
	gateway     *http.Server
	mu          sync.Mutex
	connections map[[2]guid.ID]*previewConnection
	next        func() (int64, error)
	validate    func(context.Context, PreviewGrant) error
	acquire     func(context.Context, guid.ID, guid.ID) (func(), error)
}
type previewStream struct {
	broker          *previewBroker
	connection      *previewConnection
	id              string
	grant           PreviewGrant
	ctx             context.Context
	cancel          context.CancelFunc
	opened          chan previewFrame
	data            chan []byte
	credits         chan struct{}
	readMu, writeMu sync.Mutex
	writeClosed     bool
	inboundMu       sync.Mutex
	current         []byte
	fin             chan struct{}
	finOnce         sync.Once
	once            sync.Once
	release         func()
	ports           []int
}

func newPreviewBroker(next func() (int64, error), validate func(context.Context, PreviewGrant) error) *previewBroker {
	return &previewBroker{connections: make(map[[2]guid.ID]*previewConnection), next: next, validate: validate}
}
func (b *previewBroker) Available(tenant, machine guid.ID) bool {
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
func (b *previewBroker) send(ctx context.Context, c *previewConnection, f previewFrame) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrUnavailable
	case c.frames <- f:
		return nil
	}
}
func (b *previewBroker) create(ctx context.Context, grant PreviewGrant, kind string) (*previewStream, error) {
	if grant.Port < 0 || grant.Port > 65535 || (grant.Port == 0 && kind != "probe") {
		return nil, ErrInvalidArgument
	}
	if err := b.validate(ctx, grant); err != nil {
		return nil, err
	}
	id, err := b.next()
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	var release func()
	if b.acquire != nil {
		release, err = b.acquire(ctx, grant.TenantID, grant.MachineID)
		if err != nil {
			cancel()
			return nil, err
		}
	}
	inserted := false
	defer func() {
		if !inserted && release != nil {
			release()
		}
	}()
	b.mu.Lock()
	c := b.connections[[2]guid.ID{grant.TenantID, grant.MachineID}]
	if c == nil || c.session != grant.RunnerSessionID || c.epoch != grant.ConnEpoch {
		b.mu.Unlock()
		cancel()
		return nil, ErrSessionStale
	}
	count := 0
	for _, s := range c.streams {
		if s.grant.ExecutionID == grant.ExecutionID {
			count++
		}
	}
	limit := grant.TunnelMaxStreams
	if limit < 16 || limit > 4096 {
		limit = 256
	}
	if len(c.streams) >= limit || count >= 64 {
		b.mu.Unlock()
		cancel()
		return nil, ErrSessionActive
	}
	s := &previewStream{broker: b, connection: c, id: strconv.FormatInt(id, 10), grant: grant, ctx: streamCtx, cancel: cancel, opened: make(chan previewFrame, 1), data: make(chan []byte, 16), credits: make(chan struct{}, 16), fin: make(chan struct{})}
	s.release = release
	for i := 0; i < 16; i++ {
		s.credits <- struct{}{}
	}
	c.streams[s.id] = s
	inserted = true
	b.mu.Unlock()
	if err = b.send(ctx, c, previewFrame{Kind: kind, ID: s.id, ExecutionID: grant.ExecutionID.String(), DispatchEpoch: grant.DispatchEpoch, Workspace: grant.Workspace, Port: grant.Port}); err != nil {
		_ = s.Close()
		return nil, err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case f := <-s.opened:
		if f.Kind != "opened" {
			_ = s.Close()
			return nil, ErrMachineForbidden
		}
		s.ports = f.Ports
		return s, nil
	case <-ctx.Done():
		_ = s.Close()
		return nil, ctx.Err()
	case <-c.done:
		_ = s.Close()
		return nil, ErrUnavailable
	case <-timer.C:
		_ = s.Close()
		return nil, ErrUnavailable
	}
}
func (b *previewBroker) open(ctx context.Context, grant PreviewGrant) (net.Conn, error) {
	return b.create(ctx, grant, "open")
}
func (b *previewBroker) Ports(ctx context.Context, grant PreviewGrant) ([]int, error) {
	s, err := b.create(ctx, grant, "probe")
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	return s.ports, nil
}
func (b *previewBroker) ResetSession(ctx context.Context, id guid.ID, reason string) error {
	b.mu.Lock()
	var all []*previewStream
	for _, c := range b.connections {
		for _, s := range c.streams {
			if s.grant.SessionID == id {
				all = append(all, s)
			}
		}
	}
	b.mu.Unlock()
	for _, s := range all {
		_ = s.Close()
	}
	return nil
}
func (s *previewStream) Read(dst []byte) (int, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if len(dst) == 0 {
		return 0, nil
	}
	for len(s.current) == 0 {
		select {
		case data := <-s.data:
			s.current = data
		case <-s.ctx.Done():
			return 0, s.ctx.Err()
		case <-s.connection.done:
			return 0, io.EOF
		case <-s.fin:
			select {
			case data := <-s.data:
				s.current = data
			default:
				return 0, io.EOF
			}
		}
	}
	n := copy(dst, s.current)
	s.current = s.current[n:]
	if len(s.current) == 0 {
		<-s.connection.pending
		if err := s.broker.send(s.ctx, s.connection, previewFrame{Kind: "window", ID: s.id}); err != nil {
			return n, err
		}
	}
	return n, nil
}
func (s *previewStream) Write(data []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeClosed {
		return 0, io.ErrClosedPipe
	}
	total := 0
	for len(data) > 0 {
		n := len(data)
		if n > 16*1024 {
			n = 16 * 1024
		}
		select {
		case <-s.ctx.Done():
			return total, s.ctx.Err()
		case <-s.connection.done:
			return total, io.ErrClosedPipe
		case <-s.credits:
		}
		if err := s.broker.send(s.ctx, s.connection, previewFrame{Kind: "data", ID: s.id, Data: append([]byte(nil), data[:n]...)}); err != nil {
			return total, err
		}
		total += n
		data = data[n:]
	}
	return total, nil
}
func (s *previewStream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.writeClosed {
		return nil
	}
	s.writeClosed = true
	return s.broker.send(s.ctx, s.connection, previewFrame{Kind: "fin", ID: s.id})
}
func (s *previewStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		if s.release != nil {
			s.release()
		}
		s.inboundMu.Lock()
		defer s.inboundMu.Unlock()
		s.readMu.Lock()
		defer s.readMu.Unlock()
		if len(s.current) > 0 {
			s.current = nil
			<-s.connection.pending
		}
		b := s.broker
		b.mu.Lock()
		delete(s.connection.streams, s.id)
		c := s.connection
		c.closed[s.id] = true
		c.closedOrder = append(c.closedOrder, s.id)
		if len(c.closedOrder) > 512 {
			delete(c.closed, c.closedOrder[0])
			c.closedOrder = c.closedOrder[1:]
		}
		b.mu.Unlock()
		// Reserve no unbounded goroutine for close; a saturated control queue fails closed.
		select {
		case c.frames <- previewFrame{Kind: "reset", ID: s.id}:
		case <-c.done:
		default:
			c.once.Do(func() { close(c.done) })
		}
		for {
			select {
			case <-s.data:
				<-c.pending
			default:
				return
			}
		}
	})
	return nil
}
func (*previewStream) LocalAddr() net.Addr              { return previewAddr("gateway") }
func (*previewStream) RemoteAddr() net.Addr             { return previewAddr("runner-loopback") }
func (*previewStream) SetDeadline(time.Time) error      { return nil }
func (*previewStream) SetReadDeadline(time.Time) error  { return nil }
func (*previewStream) SetWriteDeadline(time.Time) error { return nil }

type previewAddr string

func (previewAddr) Network() string  { return "preview" }
func (a previewAddr) String() string { return string(a) }

func (b *previewBroker) receive(ctx context.Context, c *previewConnection, f previewFrame) error {
	if f.Kind == "pong" {
		return nil
	}
	if len(f.Data) > 16*1024 {
		return ErrInvalidArgument
	}
	b.mu.Lock()
	s := c.streams[f.ID]
	closed := c.closed[f.ID]
	b.mu.Unlock()
	if s == nil {
		if closed {
			return nil
		}
		return ErrMachineNotFound
	}
	if err := b.validate(ctx, s.grant); err != nil {
		_ = s.Close()
		return err
	}
	switch f.Kind {
	case "opened", "rejected":
		if len(f.Ports) > 65535 {
			return ErrInvalidArgument
		}
		for _, port := range f.Ports {
			if port < 1 || port > 65535 {
				return ErrInvalidArgument
			}
		}
		select {
		case s.opened <- f:
			return nil
		default:
			return ErrVersionConflict
		}
	case "window":
		select {
		case s.credits <- struct{}{}:
			return nil
		default:
			return ErrVersionConflict
		}
	case "data":
		s.inboundMu.Lock()
		defer s.inboundMu.Unlock()
		if s.ctx.Err() != nil {
			return nil
		}
		select {
		case <-s.fin:
			return ErrVersionConflict
		default:
		}
		if len(f.Data) == 0 {
			return ErrInvalidArgument
		}
		select {
		case c.pending <- struct{}{}:
		default:
			return ErrSessionActive
		}
		select {
		case s.data <- f.Data:
			return nil
		default:
			<-c.pending
			return ErrSessionActive
		}
	case "fin":
		s.inboundMu.Lock()
		defer s.inboundMu.Unlock()
		s.finOnce.Do(func() { close(s.fin) })
		return nil
	case "reset":
		return s.Close()
	default:
		return errors.New("invalid preview frame")
	}
}
