package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type terminalTunnelInput struct {
	MachineInput
	SessionID string `query:"session_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ConnEpoch int64  `query:"conn_epoch" required:"true" minimum:"1"`
}
type terminalTunnelFramesInput struct {
	MachineInput
	Body struct {
		SessionID guid.ID         `json:"session_id"`
		ConnEpoch int64           `json:"conn_epoch" minimum:"1"`
		Frames    []terminalFrame `json:"frames" minItems:"1" maxItems:"16"`
	}
}
type TerminalStreamInput struct {
	SessionID string `path:"terminal_session_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type terminalInputHTTPInput struct {
	TerminalStreamInput
	Cookie string `header:"Cookie"`
	Origin string `header:"Origin"`
	Body   struct {
		Seq  int64  `json:"seq" minimum:"1"`
		Data []byte `json:"data" minLength:"1" maxLength:"21848"`
	}
}
type terminalAccepted struct {
	Accepted bool `json:"accepted"`
}

// Transcript bytes belong exclusively in the protected recording store. Keep
// input/output and reauthentication proofs out of configurable HTTP body logs,
// including rejected or malformed requests before domain parsing.
func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, part := range strings.Split(r.URL.Path, "/") {
			if part == "terminal-sessions" || part == "terminal-tunnel" || part == "remote-access" || part == "preview-tunnel" || part == "preview-sessions" || part == "preview-access-grants" {
				transportaudit.OmitConfidentialRequest(r.Context())
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Module) registerTerminalTunnelHTTP(api huma.API) error {
	if m.terminalBroker == nil {
		m.terminalBroker = newTerminalBroker()
	}
	if m.terminal.transport.Open == nil {
		if err := m.BindTerminalTransport(TerminalTransport{Available: m.terminalBroker.available, Open: m.openTerminalStream, Close: func(_ context.Context, s TerminalSession, reason string) error {
			return m.terminalBroker.close(s, reason)
		}}); err != nil {
			return err
		}
	}
	streamResponses := map[string]*huma.Response{"200": {Description: "Ephemeral bounded NDJSON terminal frames. Disconnect resets sessions; frames are never replayed.", Content: map[string]*huma.MediaType{"application/x-ndjson": {Schema: &huma.Schema{Type: "string"}}}}}
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.terminal-tunnel.connect", Method: http.MethodGet, Path: "/machines/{machine_id}/terminal-tunnel", Summary: "Connect an outbound fenced terminal stream", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}, Responses: streamResponses}, m.terminalTunnelHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.terminal-tunnel.frame", Method: http.MethodPost, Path: "/machines/{machine_id}/terminal-tunnel/frames", Summary: "Receive sequenced runner terminal frames", Tags: []string{"machines"}, MaxBodyBytes: 512 * 1024, Errors: []int{401, 403, 404, 409, 422, 503}}, m.terminalTunnelFramesHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, huma.Operation{OperationID: "terminal-session.stream", Method: http.MethodGet, Path: "/terminal-sessions/{terminal_session_id}/stream", Summary: "Read the authorized terminal output stream", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}, Responses: streamResponses}, "terminal_session_id", m.terminalOwner, m.terminalOutputHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, huma.Operation{OperationID: "terminal-session.input", Method: http.MethodPost, Path: "/terminal-sessions/{terminal_session_id}/input", Summary: "Send recorded sequenced terminal input", Tags: []string{"machines"}, MaxBodyBytes: 32 * 1024, Errors: []int{401, 403, 404, 409, 422, 503}}, "terminal_session_id", m.terminalOwner, m.terminalInputHTTP)
	for _, path := range []string{"/machines/{machine_id}/terminal-tunnel/frames", "/terminal-sessions/{terminal_session_id}/input"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) terminalRunnerFence(ctx context.Context, machine, session guid.ID, epoch int64) (context.Context, error) {
	ctx, err := m.BindRunnerContext(ctx, machine)
	if err != nil {
		return ctx, err
	}
	db, err := m.writer()
	if err != nil {
		return ctx, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.HeartbeatOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, session, epoch)
	})
	return ctx, err
}
func (m *Module) terminalTunnelHTTP(ctx context.Context, in *terminalTunnelInput) (*huma.StreamResponse, error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	session, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.terminalRunnerFence(ctx, machine, session, in.ConnEpoch)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	c, err := m.terminalBroker.connect(authn.TenantIDFromContext(ctx), machine, session, in.ConnEpoch)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer m.resetTerminalConnection(c, "CONNECTION_LOST")
		encoder, flush := terminalStreamWriter(stream)
		if flush() != nil {
			return
		}
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stream.Context().Done():
				return
			case <-c.done:
				return
			case f := <-c.frames:
				if encoder.Encode(f) != nil || flush() != nil {
					return
				}
			case <-tick.C:
				if _, err := m.terminalRunnerFence(ctx, c.machine, c.session, c.epoch); err != nil {
					return
				}
				if m.terminalTunnelSweep(c) != nil {
					return
				}
				if encoder.Encode(terminalFrame{Kind: "ping"}) != nil || flush() != nil {
					return
				}
			}
		}
	}}, nil
}
func terminalStreamWriter(stream huma.Context) (*json.Encoder, func() error) {
	stream.SetHeader("Content-Type", "application/x-ndjson")
	stream.SetHeader("Cache-Control", "no-store")
	stream.SetHeader("X-Content-Type-Options", "nosniff")
	stream.SetHeader("X-Accel-Buffering", "no")
	stream.SetStatus(http.StatusOK)
	flush := func() error { return nil }
	if writer, ok := stream.BodyWriter().(http.ResponseWriter); ok {
		control := http.NewResponseController(writer)
		flush = func() error { _ = control.SetWriteDeadline(time.Now().Add(15 * time.Second)); return control.Flush() }
	}
	return json.NewEncoder(stream.BodyWriter()), flush
}
func (m *Module) resetTerminalConnection(c *terminalConnection, reason string) {
	for _, s := range m.terminalBroker.disconnect(c) {
		ctx, cancel := context.WithTimeout(s.human, 15*time.Second)
		_, _ = m.CloseTerminal(ctx, c.tenant, s.session.ID, reason)
		cancel()
		_ = m.terminalBroker.close(s.session, reason)
	}
}
func (m *Module) terminalTunnelFramesHTTP(ctx context.Context, in *terminalTunnelFramesInput) (*respx.Body[terminalAccepted], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.terminalRunnerFence(ctx, machine, in.Body.SessionID, in.Body.ConnEpoch)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	b := m.terminalBroker
	b.mu.Lock()
	c := b.connections[[2]guid.ID{authn.TenantIDFromContext(ctx), machine}]
	b.mu.Unlock()
	if c == nil || c.session != in.Body.SessionID || c.epoch != in.Body.ConnEpoch {
		return nil, fleetHTTPError(ctx, ErrSessionStale)
	}
	for _, f := range in.Body.Frames {
		if f.Kind == "output" && len(f.Data) <= terminalFrameLimit {
			if err = m.throttleTerminalOutput(ctx, c, len(f.Data)); err != nil {
				return nil, fleetHTTPError(ctx, err)
			}
		}
		if err = m.terminalTunnelFrame(ctx, c, f); err != nil {
			if b.discardClosedFrame(c, f) {
				continue
			}
			m.resetTerminalConnection(c, "PROTOCOL_OR_RECORDING_FAILURE")
			return nil, fleetHTTPError(ctx, err)
		}
	}
	return respx.OK(ctx, terminalAccepted{Accepted: true}), nil
}
func (m *Module) terminalOutputHTTP(ctx context.Context, in *TerminalStreamInput) (*huma.StreamResponse, error) {
	tenant := authn.TenantIDFromContext(ctx)
	id, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if err := m.AuthorizeTerminalIO(ctx, tenant, id, false); err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	s, err := m.terminalBroker.lookup(id)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	s.bufferMu.Lock()
	if s.watching {
		s.bufferMu.Unlock()
		return nil, fleetHTTPError(ctx, ErrSessionActive)
	}
	s.watching = true
	s.bufferMu.Unlock()
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() {
			s.closeOutput(true)
			cleanup, cancel := context.WithTimeout(s.human, 15*time.Second)
			defer cancel()
			_, _ = m.CloseTerminal(cleanup, tenant, id, "VIEWER_DISCONNECTED")
		}()
		encoder, flush := terminalStreamWriter(stream)
		writeOutput := func(f terminalFrame) error {
			defer s.releaseOutput()
			if err := encoder.Encode(f); err != nil {
				return err
			}
			return flush()
		}
		if flush() != nil {
			return
		}
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stream.Context().Done():
				return
			case f := <-s.output:
				if writeOutput(f) != nil {
					return
				}
			case <-s.done:
				// Preserve already-recorded final output before emitting CLOSED.
				for {
					select {
					case f := <-s.output:
						if writeOutput(f) != nil {
							return
						}
					default:
						_ = encoder.Encode(terminalFrame{Kind: "closed", SessionID: in.SessionID})
						_ = flush()
						return
					}
				}
			case <-tick.C:
				if m.AuthorizeTerminalIO(ctx, tenant, id, false) != nil {
					return
				}
				if encoder.Encode(terminalFrame{Kind: "ping"}) != nil || flush() != nil {
					return
				}
			}
		}
	}}, nil
}
func (m *Module) terminalInputHTTP(ctx context.Context, in *terminalInputHTTPInput) (*respx.Body[terminalAccepted], error) {
	if err := m.terminalCSRF(http.MethodPost, in.Cookie, in.Origin); err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	id, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if err := m.terminalTunnelInput(ctx, authn.TenantIDFromContext(ctx), id, in.Body.Seq, in.Body.Data); err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, terminalAccepted{Accepted: true}), nil
}
