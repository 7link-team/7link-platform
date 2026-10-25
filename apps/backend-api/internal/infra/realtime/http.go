package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
)

type denial struct {
	Topic  string `json:"topic"`
	Reason string `json:"reason"`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil {
		return ErrUnavailable
	}
	op := huma.Operation{OperationID: "realtime.stream", Method: http.MethodGet, Path: "/event-stream", Summary: "Stream authorized tenant event invalidations", Tags: []string{"event-stream"}, Errors: []int{400, 401, 403, 413, 429, 503}, Responses: map[string]*huma.Response{"200": {Description: "Server-sent events", Content: map[string]*huma.MediaType{"text/event-stream": {Schema: &huma.Schema{Type: "string"}}}}}, Parameters: []*huma.Param{
		{Name: "topic", In: "query", Description: "Exact tenant topics (maximum 50)", Required: true, Schema: &huma.Schema{Type: "array", Items: &huma.Schema{Type: "string"}, MaxItems: ptr(50)}},
		{Name: "last_event_id", In: "query", Description: "Last received stream sequence", Schema: &huma.Schema{Type: "string", Pattern: `^[0-9]+$`}},
		{Name: "Last-Event-ID", In: "header", Description: "Last received stream sequence (takes precedence)", Schema: &huma.Schema{Type: "string", Pattern: `^[0-9]+$`}},
	}}
	authz.RegisterTenantMemberStream(m.registrar, api, op, func(ctx huma.Context) { m.stream(api, ctx) })
	op.OperationID = "realtime.platform-inbox-stream"
	op.Path = "/principals/{principal_id}/platform-event-stream"
	op.Summary = "Stream own system notification invalidations"
	op.Tags = []string{"principals"}
	authz.RegisterPrincipalPlatformInboxStream(m.registrar, api, op, func(ctx huma.Context) { m.stream(api, ctx) })
	return nil
}
func ptr(v int) *int { return &v }
func streamError(ctx huma.Context, status, code int, message string) {
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(map[string]any{"code": code, "message": message})
}

func (m *Module) stream(api huma.API, transport huma.Context) {
	u := transport.URL()
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		streamError(transport, 400, 172400, "Invalid event stream query")
		return
	}
	topics := values["topic"]
	if len(u.RawQuery) > 4096 || len(topics) > 50 {
		streamError(transport, 413, 172413, "Event stream topic limit exceeded")
		return
	}
	if len(topics) == 0 {
		streamError(transport, 400, 172400, "At least one topic is required")
		return
	}
	ctx := transport.Context()
	tenant, principal := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	platform := transport.Operation().OperationID == "realtime.platform-inbox-stream"
	if platform {
		tenant = 0
	}
	accepted := []string{}
	denied := []denial{}
	seen := map[string]bool{}
	for _, raw := range topics {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		allowed, reason, err := m.authorize(ctx, tenant, principal, raw)
		if errors.Is(err, ErrInvalid) {
			streamError(transport, 400, 172400, "Invalid topic syntax")
			return
		}
		if err != nil {
			streamError(transport, 503, 172503, "Topic authorization unavailable")
			return
		}
		if allowed {
			accepted = append(accepted, raw)
		} else {
			denied = append(denied, denial{raw, reason})
			parsed, _, _ := parseTopic(raw)
			m.hub.denied.WithLabelValues(parsed.Kind).Inc()
		}
	}
	if len(accepted) == 0 {
		streamError(transport, 403, 172403, "All event stream topics were denied")
		return
	}
	var after *int64
	cursor := transport.Header("Last-Event-ID")
	if cursor == "" {
		cursor = values.Get("last_event_id")
	}
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n < 0 {
			streamError(transport, 400, 172400, "Invalid event cursor")
			return
		}
		after = &n
	}
	c, replay, reason, err := m.hub.connect(principal, accepted, after)
	if errors.Is(err, ErrLimit) {
		transport.SetHeader("Retry-After", "5")
		streamError(transport, 429, 172429, "Event stream connection limit exceeded")
		return
	}
	if err != nil {
		streamError(transport, 503, 172503, "Event stream unavailable")
		return
	}
	defer m.hub.disconnect(c)
	writer, ok := transport.BodyWriter().(http.ResponseWriter)
	if !ok {
		streamError(transport, 503, 172503, "Streaming transport unavailable")
		return
	}
	controller := http.NewResponseController(writer)
	m.hub.interruptWith(c, func() { _ = controller.SetWriteDeadline(time.Now().Add(800 * time.Millisecond)) })
	transport.SetHeader("Content-Type", "text/event-stream")
	transport.SetHeader("Cache-Control", "no-cache, no-transform")
	transport.SetHeader("X-Accel-Buffering", "no")
	transport.SetStatus(http.StatusOK)
	setDeadline := func() error {
		m.hub.mu.Lock()
		defer m.hub.mu.Unlock()
		if c.stopped {
			return ErrUnavailable
		}
		return controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	}
	write := func(event string, data any, id int64) error {
		if err := setDeadline(); err != nil {
			return err
		}
		if id > 0 {
			if _, err := fmt.Fprintf(writer, "id: %d\n", id); err != nil {
				return err
			}
		}
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		kind := "control"
		if eventTypeRE.MatchString(event) {
			kind = "event"
		}
		m.hub.frames.WithLabelValues(kind).Inc()
		if event == "reauth_required" {
			m.hub.dropped.WithLabelValues("reauth").Inc()
		}
		return nil
	}
	reconnect := func() {
		_ = controller.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
		_, _ = fmt.Fprintf(writer, "retry: %d\nevent: reconnect\ndata: {}\n\n", 1000+rand.IntN(2001))
		if err := controller.Flush(); err == nil {
			m.hub.frames.WithLabelValues("control").Inc()
		}
	}
	m.hub.mu.Lock()
	readyCursor := strconv.FormatInt(m.hub.high, 10)
	m.hub.mu.Unlock()
	if err = write("ready", map[string]any{"cursor": readyCursor, "heartbeat_ms": m.heartbeat.Milliseconds(), "accepted": accepted, "denied": denied}, 0); err != nil {
		return
	}
	if reason != "" {
		if err = write("resync_required", map[string]string{"reason": reason, "cursor": readyCursor}, 0); err != nil {
			return
		}
	}
	current := map[string]bool{}
	for _, topic := range accepted {
		current[topic] = true
	}
	send := func(e events.StreamEvent) error {
		if e.Mode == events.RealtimeNone {
			return nil
		}
		matched := []string{}
		seenTopics := map[string]bool{}
		for _, topic := range e.Topics {
			if current[topic] && !seenTopics[topic] {
				matched = append(matched, topic)
				seenTopics[topic] = true
			}
		}
		if len(matched) == 0 {
			return nil
		}
		frame := map[string]any{"type": e.Type, "topics": matched, "occurred_at": e.OccurredAt}
		if e.Mode == events.RealtimeFull {
			frame["data"] = e.Data
		}
		if err := write(e.Type, frame, e.Seq); err != nil {
			return err
		}
		m.hub.latency.Observe(max(0, time.Since(e.OccurredAt).Seconds()))
		return nil
	}
	for _, e := range replay {
		if err = send(e); err != nil {
			return
		}
	}
	heartbeat := time.NewTicker(m.heartbeat)
	recheck := time.NewTicker(m.reauth)
	lifetime := time.NewTimer(m.lifetime)
	defer heartbeat.Stop()
	defer recheck.Stop()
	defer lifetime.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case why := <-c.stop:
			if why == "reconnect" {
				reconnect()
			}
			return
		case <-lifetime.C:
			m.hub.dropped.WithLabelValues("lifetime").Inc()
			reconnect()
			return
		case e := <-c.queue:
			if err = send(e); err != nil {
				return
			}
		case <-heartbeat.C:
			if err = setDeadline(); err != nil {
				return
			}
			if _, err = io.WriteString(writer, ": hb\n\n"); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			m.hub.frames.WithLabelValues("heartbeat").Inc()
		case <-recheck.C:
			var refreshed context.Context
			var err error
			if platform {
				refreshed, err = m.registrar.RecheckPrincipalPlatformInboxStream(api, transport)
			} else {
				refreshed, err = m.registrar.RecheckTenantMemberStream(api, transport)
			}
			if err != nil {
				_ = write("reauth_required", map[string]any{}, 0)
				return
			}
			if authn.PrincipalIDFromContext(refreshed) != principal || (!platform && authn.TenantIDFromContext(refreshed) != tenant) {
				_ = write("reauth_required", map[string]any{}, 0)
				return
			}
			for raw := range current {
				allowed, _, err := m.authorize(refreshed, tenant, principal, raw)
				if err != nil {
					_ = write("reauth_required", map[string]any{}, 0)
					return
				}
				if !allowed {
					delete(current, raw)
					m.hub.revoke(c, raw)
					if err = write("topic_revoked", map[string]string{"topic": raw}, 0); err != nil {
						return
					}
				}
			}
			if len(current) == 0 {
				return
			}
		}
	}
}
