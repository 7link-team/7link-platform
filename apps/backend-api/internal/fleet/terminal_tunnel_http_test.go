package fleet

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestTerminalInputBindsActualSessionSelectorAndBinaryData(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	var bound terminalInputHTTPInput
	huma.Register(api, huma.Operation{OperationID: "test.terminal-input", Method: http.MethodPost, Path: "/terminal-sessions/{terminal_session_id}/input"}, func(ctx context.Context, in *terminalInputHTTPInput) (*respx.Body[bool], error) {
		bound = *in
		return respx.OK(ctx, true), nil
	})
	req := httptest.NewRequest(http.MethodPost, "/terminal-sessions/123/input", strings.NewReader(`{"seq":1,"data":"ZWNobyBwcm9vZg0="}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	require.Equal(t, "123", bound.SessionID)
	require.Equal(t, []byte("echo proof\r"), bound.Body.Data)
}

func TestTerminalRunnerHTTPStreamTransmitsFramesAndDisconnectsWithoutReplay(t *testing.T) {
	m, db, runner, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		session, e = m.OpenSessionOn(runner, tx, 7, 1, "http-stream")
		return e
	}))
	m.terminalBroker = newTerminalBroker()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithClaims(r.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})))
		})
	})
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	huma.Register(api, huma.Operation{OperationID: "test.runner-stream", Method: "GET", Path: "/machines/{machine_id}/terminal-tunnel"}, m.terminalTunnelHTTP)
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, e := client.Get(server.URL + "/machines/1/terminal-tunnel?session_id=" + session.ID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, "application/x-ndjson", response.Header.Get("Content-Type"))
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	m.terminalBroker.mu.Lock()
	c := m.terminalBroker.connections[[2]guid.ID{7, 1}]
	m.terminalBroker.mu.Unlock()
	require.NotNil(t, c)
	require.NoError(t, sendTerminalFrame(t.Context(), c, terminalFrame{Kind: "ping"}))
	var frame terminalFrame
	require.NoError(t, json.NewDecoder(response.Body).Decode(&frame))
	require.Equal(t, "ping", frame.Kind)
	duplicate, e := client.Get(server.URL + "/machines/1/terminal-tunnel?session_id=" + session.ID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	require.Equal(t, 409, duplicate.StatusCode)
	require.NoError(t, duplicate.Body.Close())
	require.NoError(t, response.Body.Close())
	require.Eventually(t, func() bool { return !m.terminalBroker.available(7, 1) }, time.Second, time.Millisecond)
	for _, in := range []*terminalTunnelInput{{MachineInput: MachineInput{MachineID: "bad"}, SessionID: session.ID.String(), ConnEpoch: 1}, {MachineInput: MachineInput{MachineID: "1"}, SessionID: "bad", ConnEpoch: 1}, {MachineInput: MachineInput{MachineID: "1"}, SessionID: session.ID.String(), ConnEpoch: 2}} {
		_, e = m.terminalTunnelHTTP(runner, in)
		require.Error(t, e)
	}
}

func TestTerminalRunnerHTTPFramesRejectStaleConnectionsAndProtocol(t *testing.T) {
	m, db, runner, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		session, e = m.OpenSessionOn(runner, tx, 7, 1, "http-frames")
		return e
	}))
	m.terminalBroker = newTerminalBroker()
	in := &terminalTunnelFramesInput{MachineInput: MachineInput{MachineID: "1"}}
	in.Body.SessionID = session.ID
	in.Body.ConnEpoch = 1
	in.Body.Frames = []terminalFrame{{Kind: "pong"}}
	_, e := m.terminalTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	c, e := m.terminalBroker.connect(7, 1, session.ID, 1)
	require.NoError(t, e)
	accepted, e := m.terminalTunnelFramesHTTP(runner, in)
	require.NoError(t, e)
	require.True(t, accepted.Body.Data.Accepted)
	in.Body.Frames = []terminalFrame{{Kind: "unknown", SessionID: "123"}}
	_, e = m.terminalTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	select {
	case <-c.done:
	default:
		t.Fatal("protocol failure left runner connected")
	}
	in.MachineID = "bad"
	_, e = m.terminalTunnelFramesHTTP(runner, in)
	require.Error(t, e)
	in.MachineID = "1"
	in.Body.ConnEpoch = 2
	_, e = m.terminalTunnelFramesHTTP(runner, in)
	require.Error(t, e)
}

func TestTerminalViewerHTTPRecordsInputAndStreamsFinalOutputBeforeClosed(t *testing.T) {
	m, db, human, row, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, human)
	_, e := m.SetTerminalAccess(human, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, e)
	b := newTerminalBroker()
	m.terminalBroker = b
	c, e := b.connect(7, 1, row.RunnerSessionID, row.ConnEpoch)
	require.NoError(t, e)
	m.terminal.transport.Close = func(_ context.Context, s TerminalSession, reason string) error { return b.close(s, reason) }
	runner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
	opening := make(chan error, 1)
	go func() { _, e := b.open(human, terminalView(row), 80, 24); opening <- e }()
	select {
	case frame := <-c.frames:
		require.Equal(t, "open", frame.Kind)
		require.NoError(t, m.terminalTunnelFrame(runner, c, terminalFrame{Kind: "opened", SessionID: row.ID.String(), Seq: 1}))
	case <-time.After(time.Second):
		t.Fatal("open missing")
	}
	require.NoError(t, <-opening)
	m.revokeBrowser.ValidateCSRF = func(method, origin, cookie string) error {
		if method != "POST" || origin != "https://console.example.test" || cookie != "browser" {
			return ErrMachineForbidden
		}
		return nil
	}
	input := &terminalInputHTTPInput{TerminalStreamInput: TerminalStreamInput{SessionID: row.ID.String()}, Cookie: "browser", Origin: "https://console.example.test"}
	input.Body.Seq = 1
	input.Body.Data = []byte("echo recorded\r")
	accepted, e := m.terminalInputHTTP(human, input)
	require.NoError(t, e)
	require.True(t, accepted.Body.Data.Accepted)
	select {
	case frame := <-c.frames:
		require.Equal(t, "input", frame.Kind)
		require.Equal(t, input.Body.Data, frame.Data)
	case <-time.After(time.Second):
		t.Fatal("recorded input missing")
	}
	_, e = m.terminalInputHTTP(human, input)
	require.Error(t, e, "duplicate input must never execute twice")
	require.NoError(t, m.terminalTunnelFrame(runner, c, terminalFrame{Kind: "output", SessionID: row.ID.String(), Seq: 2, Data: []byte("recorded\n")}))
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(terminalOwnerContext(r.Context(), 7, 11)))
		})
	})
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	huma.Register(api, huma.Operation{OperationID: "test.viewer", Method: "GET", Path: "/terminal-sessions/{terminal_session_id}/stream"}, m.terminalOutputHTTP)
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, e := client.Get(server.URL + "/terminal-sessions/" + row.ID.String() + "/stream")
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	decoder := json.NewDecoder(response.Body)
	var frame terminalFrame
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "output", frame.Kind)
	require.Equal(t, []byte("recorded\n"), frame.Data)
	_, e = m.terminalOutputHTTP(human, &TerminalStreamInput{SessionID: row.ID.String()})
	require.Error(t, e, "only one live viewer")
	require.NoError(t, m.terminalTunnelFrame(runner, c, terminalFrame{Kind: "output", SessionID: row.ID.String(), Seq: 3, Data: []byte("final\n")}))
	require.NoError(t, m.terminalTunnelFrame(runner, c, terminalFrame{Kind: "closed", SessionID: row.ID.String(), Seq: 4}))
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "output", frame.Kind)
	require.Equal(t, []byte("final\n"), frame.Data)
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "closed", frame.Kind)
	stored, e := m.GetTerminal(human, 7, row.ID)
	require.NoError(t, e)
	require.Equal(t, "CLOSED", stored.State)
	require.Equal(t, "PROCESS_EXIT", stored.CloseReason)
	require.NotNil(t, stored.RecordingFileID)
	_, e = m.terminalOutputHTTP(human, &TerminalStreamInput{SessionID: "bad"})
	require.Error(t, e)
	input.SessionID = "bad"
	_, e = m.terminalInputHTTP(human, input)
	require.Error(t, e)
}

func TestTerminalViewerHTTPCurrentAuthorityAndMissingStreamReturnNoOutput(t *testing.T) {
	for _, fault := range []string{"authority", "missing_stream", "csrf", "source", "runner_source"} {
		t.Run(fault, func(t *testing.T) {
			m, db, human, row, _ := terminalFixture(t)
			_, err := m.SetTerminalAccess(human, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			m.terminalBroker = newTerminalBroker()
			m.revokeBrowser.ValidateCSRF = func(string, string, string) error { return nil }
			switch fault {
			case "authority":
				m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
					return TerminalExecution{}, ErrMachineForbidden
				}
			case "csrf":
				m.revokeBrowser.ValidateCSRF = func(string, string, string) error { return ErrMachineForbidden }
			case "source", "runner_source":
				actual, e := m.writer()
				require.NoError(t, e)
				require.NoError(t, actual.Callback().Query().Before("gorm:query").Register("terminal_http_current_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.TerminalSessions.String() || tx.Statement.Table == m.names.Machines.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer func() { require.NoError(t, actual.Callback().Query().Remove("terminal_http_current_failure")) }()
			}
			if fault == "csrf" {
				input := &terminalInputHTTPInput{TerminalStreamInput: TerminalStreamInput{SessionID: row.ID.String()}, Cookie: "browser", Origin: "https://console.example.test"}
				input.Body.Seq = 1
				input.Body.Data = []byte("private input")
				response, e := m.terminalInputHTTP(human, input)
				require.Error(t, e)
				require.Nil(t, response)
			} else if fault == "runner_source" {
				runner := authn.WithClaims(human, &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
				response, e := m.terminalTunnelHTTP(runner, &terminalTunnelInput{MachineInput: MachineInput{MachineID: "1"}, SessionID: row.RunnerSessionID.String(), ConnEpoch: row.ConnEpoch})
				require.Error(t, e)
				require.Nil(t, response)
			} else {
				response, e := m.terminalOutputHTTP(human, &TerminalStreamInput{SessionID: row.ID.String()})
				require.Error(t, e)
				require.Nil(t, response)
			}
			if fault == "source" || fault == "runner_source" {
				actual, e := m.writer()
				require.NoError(t, e)
				require.NoError(t, actual.Callback().Query().Remove("terminal_http_current_failure"))
			}
			var stored struct{ State, RecordingChunks string }
			require.NoError(t, db.Table(m.names.TerminalSessions.String()).Where("id=?", row.ID).Take(&stored).Error)
			require.Equal(t, "OPEN", stored.State)
			require.Equal(t, "[]", stored.RecordingChunks)
		})
	}
}

func TestTerminalRunnerHTTPKeepaliveRechecksCurrentIAMAndDisconnects(t *testing.T) {
	m, db, runner, _ := machineHarness(t)
	var session SessionFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		session, e = m.OpenSessionOn(runner, tx, 7, 1, "keepalive-transport")
		return e
	}))
	m.terminalBroker = newTerminalBroker()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authn.WithClaims(r.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})))
		})
	})
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	huma.Register(api, huma.Operation{OperationID: "test.runner-keepalive", Method: "GET", Path: "/machines/{machine_id}/terminal-tunnel"}, m.terminalTunnelHTTP)
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 8 * time.Second
	response, e := client.Get(server.URL + "/machines/1/terminal-tunnel?session_id=" + session.ID.String() + "&conn_epoch=1")
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	decoder := json.NewDecoder(response.Body)
	var frame terminalFrame
	require.NoError(t, decoder.Decode(&frame))
	require.Equal(t, "ping", frame.Kind)
	// Revoke the actual persisted machine binding; the next heartbeat must fail.
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Update("state", "OFFLINE").Error)
	require.Error(t, decoder.Decode(&frame))
	require.Eventually(t, func() bool { return !m.terminalBroker.available(7, 1) }, time.Second, time.Millisecond)
}
