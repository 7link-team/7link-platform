package realtime

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stretchr/testify/require"
)

type streamVerifier struct{ invalid, oauth, inactive, noPlatform atomic.Bool }

func (v *streamVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	if v.invalid.Load() {
		return nil, authn.ErrInvalidToken
	}
	c := &authn.Claims{PrincipalID: 10, Subject: "10"}
	if v.oauth.Load() {
		c.ClientID = "delegated"
	}
	return c, nil
}
func (v *streamVerifier) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return !v.inactive.Load(), nil
}
func (*streamVerifier) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (v *streamVerifier) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return !v.noPlatform.Load(), nil
}

func TestPlatformInboxRealStreamOwnershipAndAuthorityRevocation(t *testing.T) {
	m, verifier, server := streamHarness(t)
	response := requestStream(t, server, "/principals/10/platform-event-stream?topic=tenant/0/inbox/10")
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	reader := bufio.NewReader(response.Body)
	require.Contains(t, readFrame(t, reader), "tenant/0/inbox/10")
	require.NoError(t, m.Broadcast(t.Context(), "tenant/0/inbox/10", Message{Type: "notification.created", Payload: map[string]any{}}))
	// Deliver a real hub hint, as sequenced ephemeral broadcasts do in runtime.
	m.hub.deliver(events.StreamEvent{Seq: 1, TenantID: 0, Type: "notification.created", Topics: []string{"tenant/0/inbox/10"}, Mode: events.RealtimeFull, Data: json.RawMessage(`{"payload":{}}`)})
	require.Contains(t, untilFrame(t, reader, "notification.created"), `"payload":{}`)
	verifier.noPlatform.Store(true)
	require.Contains(t, untilFrame(t, reader, "reauth_required"), "reauth_required")
	denied := requestStream(t, server, "/principals/10/platform-event-stream?topic=tenant/0/inbox/10")
	defer denied.Body.Close()
	require.Equal(t, 403, denied.StatusCode)
	verifier.noPlatform.Store(false)
	foreign := requestStream(t, server, "/principals/11/platform-event-stream?topic=tenant/0/inbox/11")
	defer foreign.Body.Close()
	require.Equal(t, 404, foreign.StatusCode)
	otherInbox := requestStream(t, server, "/principals/10/platform-event-stream?topic=tenant/0/inbox/11")
	defer otherInbox.Body.Close()
	require.Equal(t, 403, otherInbox.StatusCode)
	tenantTopic := requestStream(t, server, "/principals/10/platform-event-stream?topic=tenant/7/inbox/10")
	defer tenantTopic.Body.Close()
	require.Equal(t, 403, tenantTopic.StatusCode)
}

func streamHarness(t *testing.T) (*Module, *streamVerifier, *httptest.Server) {
	t.Helper()
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), Config{})
	v := &streamVerifier{}
	m.registrar = authz.NewRegistrar(authz.MustRegistry(), v, v, v)
	m.heartbeat = 25 * time.Millisecond
	m.reauth = 40 * time.Millisecond
	m.lifetime = 3 * time.Second
	// The stream is tested over a real TCP connection, including flushing and
	// deadlines, instead of a recorder that buffers the response until return.
	m.hub = activeHub()
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	server := httptest.NewServer(mux)
	t.Cleanup(func() { require.NoError(t, m.Quiesce(context.Background())); server.Close() })
	return m, v, server
}
func requestStream(t *testing.T, s *httptest.Server, path string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer original")
	response, err := s.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}
func readFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err, b.String())
		b.WriteString(line)
		if line == "\n" {
			return b.String()
		}
	}
}
func untilFrame(t *testing.T, r *bufio.Reader, kind string) string {
	t.Helper()
	for i := 0; i < 30; i++ {
		frame := readFrame(t, r)
		if strings.Contains(frame, "event: "+kind+"\n") {
			return frame
		}
	}
	t.Fatalf("no %s frame", kind)
	return ""
}

func TestSSEReplaySignalFullPartialDenialAndQuiesce(t *testing.T) {
	m, _, s := streamHarness(t)
	topic := "tenant/1/inbox/10"
	e := signalEvent(1, topic)
	e.Data = json.RawMessage(`{"secret":"must-not-leak"}`)
	m.hub.deliver(e)
	e = signalEvent(2, topic)
	e.Mode = events.RealtimeFull
	e.Data = json.RawMessage(`{"count":2}`)
	m.hub.deliver(e)
	response := requestStream(t, s, "/tenants/1/event-stream?topic="+topic+"&topic=tenant/2/inbox/10&last_event_id=0")
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, "no", response.Header.Get("X-Accel-Buffering"))
	require.Contains(t, response.Header.Get("Cache-Control"), "no-transform")
	r := bufio.NewReader(response.Body)
	ready := readFrame(t, r)
	require.Contains(t, ready, "tenant_mismatch")
	signal := readFrame(t, r)
	require.Contains(t, signal, "id: 1\n")
	require.NotContains(t, signal, "secret")
	require.NotContains(t, signal, `"data"`)
	full := readFrame(t, r)
	require.Contains(t, full, "id: 2\n")
	require.Contains(t, full, `"count":2`)
	m.hub.deliver(signalEvent(3, topic, topic))
	live := untilFrame(t, r, "work.changed")
	require.Contains(t, live, "id: 3\n")
	require.Equal(t, 1, strings.Count(live, topic))
	require.Contains(t, readFrame(t, r), ": hb")
	start := time.Now()
	require.NoError(t, m.Quiesce(t.Context()))
	require.Contains(t, untilFrame(t, r, "reconnect"), "retry: ")
	_, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second)
}

func TestSSEReauthTopicRevocationAndLifetime(t *testing.T) {
	for _, scenario := range []string{"logout", "membership", "topic", "lifetime"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, s := streamHarness(t)
			var revoked atomic.Bool
			require.NoError(t, m.register("project", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return !revoked.Load(), nil }))
			if scenario == "lifetime" {
				m.lifetime = 80 * time.Millisecond
			}
			response := requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/project/2")
			defer response.Body.Close()
			require.Equal(t, 200, response.StatusCode)
			r := bufio.NewReader(response.Body)
			require.Contains(t, readFrame(t, r), "event: ready")
			want := "reauth_required"
			switch scenario {
			case "logout":
				v.invalid.Store(true)
			case "membership":
				v.inactive.Store(true)
			case "topic":
				revoked.Store(true)
				want = "topic_revoked"
			case "lifetime":
				want = "reconnect"
			}
			frame := untilFrame(t, r, want)
			if scenario == "topic" {
				require.Contains(t, frame, "tenant/1/project/2")
			}
			_, err := io.ReadAll(response.Body)
			require.NoError(t, err)
		})
	}
}

func TestSSEBadRequestsAndLimits(t *testing.T) {
	m, v, s := streamHarness(t)
	for _, test := range []struct {
		query  string
		status int
	}{{"", 400}, {"?topic=tenant/1/work-items/2", 400}, {"?topic=tenant/1/inbox/11", 403}, {"?topic=tenant/1/inbox/10/channel", 403}, {"?topic=tenant/1/unknown/2", 403}, {"?topic=tenant/1/inbox/10&last_event_id=-1", 400}, {"?topic=tenant/1/inbox/10&x=" + strings.Repeat("a", 4096), 413}, {"?" + strings.Repeat("topic=tenant/1/inbox/10&", 51), 413}} {
		response := requestStream(t, s, "/tenants/1/event-stream"+test.query)
		require.Equal(t, test.status, response.StatusCode, test.query)
		_ = response.Body.Close()
	}
	v.oauth.Store(true)
	response := requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10")
	require.Equal(t, 403, response.StatusCode)
	_ = response.Body.Close()
	v.oauth.Store(false)
	for i := 0; i < 10; i++ {
		response = requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10")
		defer response.Body.Close()
		require.Equal(t, 200, response.StatusCode)
	}
	response = requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10")
	defer response.Body.Close()
	require.Equal(t, 429, response.StatusCode)
	require.Equal(t, "5", response.Header.Get("Retry-After"))
	// Quiesce closes all open test connections before the HTTP server shuts down.
	require.NoError(t, m.Quiesce(t.Context()))
}

func TestSSEUnknownAndExpiredCursors(t *testing.T) {
	for _, scenario := range []struct {
		cursor string
		reason string
	}{{"99", "cursor_unknown"}, {"0", "cursor_too_old"}} {
		t.Run(scenario.reason, func(t *testing.T) {
			m, _, s := streamHarness(t)
			m.hub.deliver(signalEvent(10, "tenant/1/inbox/10"))
			response := requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10&last_event_id="+scenario.cursor)
			defer response.Body.Close()
			r := bufio.NewReader(response.Body)
			readFrame(t, r)
			require.Contains(t, readFrame(t, r), scenario.reason)
		})
	}
}

func TestSSEAuthorizationDependencyFailureAndUnavailableHub(t *testing.T) {
	m, _, s := streamHarness(t)
	require.NoError(t, m.register("project", func(context.Context, guid.ID, guid.ID, Topic) (bool, error) { return false, ErrUnavailable }))
	response := requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/project/2")
	require.Equal(t, 503, response.StatusCode)
	_ = response.Body.Close()
	response = requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10&bad=%zz")
	require.Equal(t, 400, response.StatusCode)
	_ = response.Body.Close()
	require.NoError(t, m.Quiesce(t.Context()))
	response = requestStream(t, s, "/tenants/1/event-stream?topic=tenant/1/inbox/10")
	require.Equal(t, 503, response.StatusCode)
	_ = response.Body.Close()
}
