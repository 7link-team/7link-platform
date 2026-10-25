package fleet

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type previewTunnelFramesInput struct {
	MachineInput
	Body struct {
		SessionID guid.ID        `json:"session_id"`
		ConnEpoch int64          `json:"conn_epoch" minimum:"1"`
		Frames    []previewFrame `json:"frames" minItems:"1" maxItems:"16"`
	}
}

func (m *Module) initializePreview(context.Context) error {
	if err := m.config.Preview.Validate(); err != nil {
		return err
	}
	m.previewBroker = newPreviewBroker(m.ids.Next, m.validatePreviewGrant)
	m.previewBroker.acquire = m.acquireTunnelStream
	m.previewTransport = &PreviewRuntime{Ports: m.previewBroker.Ports, Available: m.previewBroker.Available, ResetSession: m.previewBroker.ResetSession}
	return nil
}
func (m *Module) validatePreviewGrant(ctx context.Context, g PreviewGrant) error {
	if !g.ExpiresAt.IsZero() && !time.Now().Before(g.ExpiresAt) {
		return ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		facts, err := m.previewMachineOn(ctx, tx, g.TenantID, g.MachineID)
		if err != nil {
			return err
		}
		if !facts.Enabled {
			return ErrTerminalAccessDisabled
		}
		if facts.RunnerSessionID != g.RunnerSessionID || facts.ConnEpoch != g.ConnEpoch {
			return ErrSessionStale
		}
		if facts.State != "ONLINE" && facts.State != "DRAINING" {
			return ErrMachineOffline
		}
		return nil
	})
	if err != nil {
		return err
	}
	if g.SessionID > 0 {
		if m.previewAccess.Revalidate == nil {
			return ErrUnavailable
		}
		return m.previewAccess.Revalidate(ctx, g)
	}
	return nil
}
func (m *Module) registerPreviewTunnelHTTP(api huma.API) error {
	streamResponses := map[string]*huma.Response{"200": {Description: "Ephemeral bounded NDJSON preview tunnel frames", Content: map[string]*huma.MediaType{"application/x-ndjson": {Schema: &huma.Schema{Type: "string"}}}}}
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.preview-tunnel.connect", Method: "GET", Path: "/machines/{machine_id}/preview-tunnel", Summary: "Connect a fenced outbound preview tunnel", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 429, 503}, Responses: streamResponses}, m.previewTunnelHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.preview-tunnel.frame", Method: "POST", Path: "/machines/{machine_id}/preview-tunnel/frames", Summary: "Receive bounded preview tunnel frames", Tags: []string{"machines"}, MaxBodyBytes: 512 * 1024, Errors: []int{401, 403, 404, 409, 422, 429, 503}}, m.previewTunnelFramesHTTP)
	return authz.RegisterRequestContract(api, "/machines/{machine_id}/preview-tunnel/frames")
}
func (m *Module) previewTunnelHTTP(ctx context.Context, in *terminalTunnelInput) (*huma.StreamResponse, error) {
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
	b := m.previewBroker
	b.mu.Lock()
	key := [2]guid.ID{authn.TenantIDFromContext(ctx), machine}
	if b.connections[key] != nil {
		b.mu.Unlock()
		return nil, fleetHTTPError(ctx, ErrSessionActive)
	}
	c := &previewConnection{tenant: key[0], machine: machine, session: session, epoch: in.ConnEpoch, frames: make(chan previewFrame, 256), done: make(chan struct{}), streams: make(map[string]*previewStream), closed: make(map[string]bool), pending: make(chan struct{}, 256)}
	b.connections[key] = c
	b.mu.Unlock()
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() {
			c.once.Do(func() { close(c.done) })
			b.mu.Lock()
			delete(b.connections, key)
			var all []*previewStream
			for _, s := range c.streams {
				all = append(all, s)
			}
			b.mu.Unlock()
			for _, s := range all {
				_ = s.Close()
			}
		}()
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
				if _, err := m.terminalRunnerFence(ctx, machine, session, c.epoch); err != nil {
					return
				}
				b.mu.Lock()
				var all []*previewStream
				for _, s := range c.streams {
					all = append(all, s)
				}
				b.mu.Unlock()
				for _, s := range all {
					if b.validate(ctx, s.grant) != nil {
						_ = s.Close()
					}
				}
				if encoder.Encode(previewFrame{Kind: "ping"}) != nil || flush() != nil {
					return
				}
			}
		}
	}}, nil
}
func (m *Module) previewTunnelFramesHTTP(ctx context.Context, in *previewTunnelFramesInput) (*respx.Body[terminalAccepted], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.terminalRunnerFence(ctx, machine, in.Body.SessionID, in.Body.ConnEpoch)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	b := m.previewBroker
	b.mu.Lock()
	c := b.connections[[2]guid.ID{authn.TenantIDFromContext(ctx), machine}]
	b.mu.Unlock()
	if c == nil || c.session != in.Body.SessionID || c.epoch != in.Body.ConnEpoch {
		return nil, fleetHTTPError(ctx, ErrSessionStale)
	}
	for _, f := range in.Body.Frames {
		if f.Kind == "data" {
			if len(f.Data) == 0 || len(f.Data) > 16*1024 {
				return nil, fleetHTTPError(ctx, ErrInvalidArgument)
			}
			if err = m.throttleTunnelOutput(ctx, c.tenant, c.machine, c.done, len(f.Data), "preview"); err != nil {
				return nil, fleetHTTPError(ctx, err)
			}
		}
		if err = b.receive(ctx, c, f); err != nil {
			c.once.Do(func() { close(c.done) })
			return nil, fleetHTTPError(ctx, err)
		}
	}
	return respx.OK(ctx, terminalAccepted{Accepted: true}), nil
}
