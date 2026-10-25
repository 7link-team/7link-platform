package fleet

import (
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestPreviewTunnelFramesBindFenceAndBinaryDataAndRejectOversizedBatch(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	var bound previewTunnelFramesInput
	calls := 0
	huma.Register(api, huma.Operation{OperationID: "test.preview-frames", Method: http.MethodPost, Path: "/machines/{machine_id}/preview-tunnel/frames"}, func(ctx context.Context, in *previewTunnelFramesInput) (*respx.Body[bool], error) {
		bound = *in
		calls++
		return respx.OK(ctx, true), nil
	})
	request := func(frames []previewFrame) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"session_id": guid.ID(456), "conn_epoch": 7, "frames": frames})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/machines/123/preview-tunnel/frames", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	frame := previewFrame{Kind: "data", ID: "stream", Data: []byte{0, 255, 1}}
	accepted := request([]previewFrame{frame})
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	require.Equal(t, "123", bound.MachineID)
	require.Equal(t, guid.ID(456), bound.Body.SessionID)
	require.Equal(t, int64(7), bound.Body.ConnEpoch)
	require.Equal(t, []previewFrame{frame}, bound.Body.Frames)
	denied := request(make([]previewFrame, 17))
	require.Equal(t, http.StatusUnprocessableEntity, denied.Code, denied.Body.String())
	require.Equal(t, 1, calls, "oversized batches must be rejected before the handler")
}

func TestPreviewRunnerHTTPStreamAndFramesUseCurrentRunnerFence(t *testing.T) {
	m, db, runner, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		session, e = m.OpenSessionOn(runner, tx, 7, 1, "preview-http")
		return e
	}))
	m.terminalBroker = newTerminalBroker()
	require.NoError(t, m.initializePreview(t.Context()))
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithClaims(r.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})))
		})
	})
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	huma.Register(api, huma.Operation{OperationID: "test.preview-runner", Method: "GET", Path: "/machines/{machine_id}/preview-tunnel"}, m.previewTunnelHTTP)
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, e := client.Get(server.URL + "/machines/1/preview-tunnel?session_id=" + session.ID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	b := m.previewBroker
	b.mu.Lock()
	c := b.connections[[2]guid.ID{7, 1}]
	b.mu.Unlock()
	require.NotNil(t, c)
	require.NoError(t, b.send(t.Context(), c, previewFrame{Kind: "ping"}))
	var frame previewFrame
	require.NoError(t, json.NewDecoder(response.Body).Decode(&frame))
	require.Equal(t, "ping", frame.Kind)
	duplicate, e := client.Get(server.URL + "/machines/1/preview-tunnel?session_id=" + session.ID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	require.Equal(t, 409, duplicate.StatusCode)
	require.NoError(t, duplicate.Body.Close())
	in := &previewTunnelFramesInput{MachineInput: MachineInput{MachineID: "1"}}
	in.Body.SessionID = session.ID
	in.Body.ConnEpoch = 1
	in.Body.Frames = []previewFrame{{Kind: "pong"}}
	accepted, e := m.previewTunnelFramesHTTP(runner, in)
	require.NoError(t, e)
	require.True(t, accepted.Body.Data.Accepted)
	for _, data := range [][]byte{nil, make([]byte, 16*1024+1)} {
		in.Body.Frames = []previewFrame{{Kind: "data", ID: "missing", Data: data}}
		_, e = m.previewTunnelFramesHTTP(runner, in)
		require.Error(t, e)
	}
	in.Body.Frames = []previewFrame{{Kind: "unknown", ID: "missing"}}
	_, e = m.previewTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	require.NoError(t, response.Body.Close())
	require.Eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.connections[[2]guid.ID{7, 1}] == nil }, time.Second, time.Millisecond)
	in.Body.Frames = []previewFrame{{Kind: "pong"}}
	_, e = m.previewTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	in.MachineID = "bad"
	_, e = m.previewTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	in.MachineID = "1"
	in.Body.ConnEpoch = 2
	_, e = m.previewTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	for _, request := range []*terminalTunnelInput{{MachineInput: MachineInput{MachineID: "bad"}, SessionID: session.ID.String(), ConnEpoch: 1}, {MachineInput: MachineInput{MachineID: "1"}, SessionID: "bad", ConnEpoch: 1}, {MachineInput: MachineInput{MachineID: "1"}, SessionID: session.ID.String(), ConnEpoch: 2}} {
		_, e = m.previewTunnelHTTP(runner, request)
		require.Error(t, e)
	}
}

func TestPreviewGrantRevalidatesActualAccessEpochAndCurrentSessionAuthority(t *testing.T) {
	m, db, human, row, _ := terminalFixture(t)
	g := PreviewGrant{TenantID: 7, MachineID: 1, RunnerSessionID: row.RunnerSessionID, ConnEpoch: row.ConnEpoch, Port: 3000}
	require.ErrorIs(t, m.validatePreviewGrant(human, g), ErrTerminalAccessDisabled)
	enabled := true
	_, e := m.SetTerminalAccessOptions(human, 7, 1, 1, TerminalAccessOptions{AllowRemotePreview: &enabled}, "", "")
	require.NoError(t, e)
	require.NoError(t, m.validatePreviewGrant(human, g))
	changed := g
	changed.ConnEpoch++
	require.ErrorIs(t, m.validatePreviewGrant(human, changed), ErrSessionStale)
	changed = g
	changed.ExpiresAt = time.Now().Add(-time.Second)
	require.ErrorIs(t, m.validatePreviewGrant(human, changed), ErrUnavailable)
	changed = g
	changed.SessionID = 901
	require.ErrorIs(t, m.validatePreviewGrant(human, changed), ErrUnavailable)
	m.previewAccess.Revalidate = func(context.Context, PreviewGrant) error { return ErrMachineForbidden }
	require.ErrorIs(t, m.validatePreviewGrant(human, changed), ErrMachineForbidden)
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id=1").Update("state", "OFFLINE").Error)
	require.ErrorIs(t, m.validatePreviewGrant(human, g), ErrMachineOffline)
	require.NoError(t, db.Exec("DROP TABLE "+m.names.TerminalAccess.String()).Error)
	require.Error(t, m.validatePreviewGrant(human, g))
}

func TestPreviewRunnerHTTPKeepaliveRevalidatesLiveGrantAndReleasesStream(t *testing.T) {
	m, db, human, row, _ := terminalFixture(t)
	enabled := true
	_, e := m.SetTerminalAccessOptions(human, 7, 1, 1, TerminalAccessOptions{AllowRemotePreview: &enabled}, "", "")
	require.NoError(t, e)
	broker := newPreviewBroker(m.ids.Next, m.validatePreviewGrant)
	m.previewBroker = broker
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithClaims(r.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})))
		})
	})
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	huma.Register(api, huma.Operation{OperationID: "test.preview-keepalive", Method: "GET", Path: "/machines/{machine_id}/preview-tunnel"}, m.previewTunnelHTTP)
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 8 * time.Second
	response, e := client.Get(server.URL + "/machines/1/preview-tunnel?session_id=" + row.RunnerSessionID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	decoder := json.NewDecoder(response.Body)
	var frame previewFrame
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "ping", frame.Kind)
	broker.mu.Lock()
	c := broker.connections[[2]guid.ID{7, 1}]
	broker.mu.Unlock()
	require.NotNil(t, c)
	grant := PreviewGrant{TenantID: 7, MachineID: 1, RunnerSessionID: row.RunnerSessionID, ConnEpoch: row.ConnEpoch, ExecutionID: 91, Port: 3000}
	opened := make(chan *previewStream, 1)
	failed := make(chan error, 1)
	go func() {
		s, e := broker.create(t.Context(), grant, "open")
		if e != nil {
			failed <- e
			return
		}
		opened <- s
	}()
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "open", frame.Kind)
	require.NoError(t, broker.receive(t.Context(), c, previewFrame{Kind: "opened", ID: frame.ID}))
	var stream *previewStream
	select {
	case stream = <-opened:
	case e := <-failed:
		t.Fatal(e)
	case <-time.After(time.Second):
		t.Fatal("grant open not acknowledged")
	}
	// The real current access policy changes; the server sweep must release this stream.
	disabled := false
	_, e = m.SetTerminalAccessOptions(human, 7, 1, 2, TerminalAccessOptions{AllowRemotePreview: &disabled}, "", "")
	require.NoError(t, e)
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "ping", frame.Kind)
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "reset", frame.Kind)
	require.Error(t, stream.ctx.Err())
	broker.mu.Lock()
	require.Empty(t, c.streams)
	broker.mu.Unlock()
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
	require.Error(t, decoder.Decode(&frame))
}
