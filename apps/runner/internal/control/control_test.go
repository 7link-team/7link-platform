package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
	"7link.com/runner/internal/inspection"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return s
}
func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}
func testProfile(server string) Profile {
	return Profile{Server: server, TenantID: "7", MachineID: "11", ClientID: "12", Secret: "machine-secret-private"}
}

func TestDaemonRefreshesVersionRejectedTokenFromProtectedCurrentCredential(t *testing.T) {
	store := testStore(t)
	heartbeats, tokens, pulls := 0, 0, 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/discovery":
			respond(w, true)
		case "/oauth/token":
			tokens++
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "32" || r.Form.Get("client_secret") != "current-rotated-private" {
				t.Error("refresh did not use protected current credential")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "current-version-token", "token_type": "Bearer", "expires_in": 3600})
		case "/machines/11/heartbeat":
			heartbeats++
			if r.Header.Get("Authorization") == "Bearer cached-prior-version" {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("Authorization") != "Bearer current-version-token" {
				t.Error("heartbeat missing refreshed authority")
			}
			respond(w, true)
		case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/credential-deliveries", "/machines/11/inspection-pulls":
			respond(w, []any{})
		case "/runner/execution-pulls":
			pulls++
			respond(w, []command{})
			cancel()
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := testProfile(server.URL)
	p.ClientID = "32"
	p.Secret = "current-rotated-private"
	if err := store.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient(server.URL)
	c.store = store
	c.token = "cached-prior-version"
	c.expires = time.Now().Add(time.Hour)
	err := daemonLoop(ctx, store, c, p, fence{}, &journal{}, func(context.Context, executor.Plan, func(executor.Observation) error) (executor.Result, error) {
		t.Error("runtime launched")
		return executor.Result{}, nil
	})
	if !errors.Is(err, context.Canceled) || heartbeats < 2 || tokens != 1 || pulls != 1 {
		t.Fatal("daemon did not continue after account-version refresh", err, heartbeats, tokens, pulls)
	}
}

func TestAuthenticated401RetryIsBoundedAnd503NeverReplays(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := testStore(t)
			requests, tokens := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					tokens++
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "token_type": "Bearer", "expires_in": 3600})
					return
				}
				requests++
				w.WriteHeader(status)
			}))
			defer server.Close()
			p := testProfile(server.URL)
			if err := store.SaveProfile(p); err != nil {
				t.Fatal(err)
			}
			c, _ := NewClient(server.URL)
			c.store = store
			c.token = "cached"
			c.expires = time.Now().Add(time.Hour)
			err := c.JSON(t.Context(), "POST", "/test", map[string]string{"command": "one"}, nil, true)
			var api *APIError
			if !errors.As(err, &api) || api.Status != status {
				t.Fatal("wrong terminal error", err)
			}
			wantRequests, wantTokens := 1, 0
			if status == 401 {
				wantRequests, wantTokens = 2, 1
			}
			if requests != wantRequests || tokens != wantTokens {
				t.Fatal("unbounded or unsafe retry", requests, tokens)
			}
		})
	}
}

func TestLoginExchangesMachineCredentialAndNeverPrintsSecrets(t *testing.T) {
	store := testStore(t)
	exchanges := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/discovery":
			respond(w, true)
		case "/machine-enrollments":
			var in struct {
				Tenant string          `json:"tenant_id"`
				Client string          `json:"client_id"`
				OS     string          `json:"os"`
				Facts  enrollmentFacts `json:"facts"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Tenant != "7" || in.Client != "8" || in.OS == "" || in.Facts.Hostname != "observed-host" || in.Facts.Arch != "amd64" || len(in.Facts.Runtimes) != 2 || !in.Facts.Runtimes[0].Available || in.Facts.Runtimes[1].Error != "NOT_FOUND" {
				t.Error("wrong enrollment identity or observed facts", in)
			}
			w.WriteHeader(201)
			respond(w, map[string]any{"id": "9", "device": map[string]any{"device_code": "private-device-code", "user_code": "ABCD-EFGH", "verification_uri": "https://console.example/device", "interval": 1, "expires_in": 30}})
		case "/machine-enrollments/9/credential":
			exchanges++
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["device_code"] != "private-device-code" || in["tenant_id"] != "7" {
				t.Error("bad device proof")
			}
			if exchanges == 1 {
				w.WriteHeader(409)
				respond(w, map[string]string{"reason_code": "AUTHORIZATION_PENDING"})
				return
			}
			respond(w, map[string]any{"machine_id": "11", "credential": map[string]any{"credential": map[string]string{"id": "12"}, "client_secret": "machine-secret-private"}})
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "12" || r.Form.Get("client_secret") != "machine-secret-private" || r.Form.Get("grant_type") != "client_credentials" {
				t.Error("not using the machine IAM credential")
			}
			// Credential must already be durable before authentication.
			if _, e := store.Profile(); e != nil {
				t.Error(e)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-access-token", "token_type": "Bearer", "expires_in": 60})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	if e := login(t.Context(), store, LoginOptions{Server: server.URL, TenantID: "7", ClientID: "8", Name: "Test machine"}, &output, enrollmentTestObservations); e != nil {
		t.Fatal(e)
	}
	if exchanges != 2 {
		t.Fatal("authorization pending was not polled")
	}
	if !strings.Contains(output.String(), "ABCD-EFGH") {
		t.Fatal("user code absent")
	}
	for _, secret := range []string{"machine-secret-private", "private-device-code", "private-access-token"} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("secret printed")
		}
	}
	p, e := store.Profile()
	if e != nil || p.ClientID != "12" {
		t.Fatal(p.ClientID, e)
	}
	output.Reset()
	if e = Status(store, &output); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(output.String(), p.Secret) {
		t.Fatal("status exposed credential")
	}
	data, e := os.ReadFile(filepath.Join(store.Dir, "identity.json"))
	if e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS == "windows" && bytes.Contains(data, []byte(p.Secret)) {
		t.Fatal("Windows profile was not DPAPI protected")
	}
	if e = Login(t.Context(), store, LoginOptions{Server: server.URL, TenantID: "7", ClientID: "8", Name: "Again"}, io.Discard); e == nil {
		t.Fatal("existing identity overwritten")
	}
}

func TestProfileLockAndAtomicPrivateRoundTrip(t *testing.T) {
	store := testStore(t)
	if other, e := OpenStore(store.Dir); e == nil {
		other.Close()
		t.Fatal("same profile may only have one owner")
	}
	p := testProfile("https://api.example")
	if e := store.SaveProfile(p); e != nil {
		t.Fatal(e)
	}
	p.Secret = "rotated-secret"
	if e := store.SaveProfile(p); e != nil {
		t.Fatal("atomic replacement failed", e)
	}
	store.Close()
	other, e := OpenStore(store.Dir)
	if e != nil {
		t.Fatal("lock not released", e)
	}
	defer other.Close()
	loaded, e := other.Profile()
	if e != nil || loaded != p {
		t.Fatal("profile roundtrip failed", e)
	}
	entries, e := os.ReadDir(store.Dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			t.Fatal("temporary credential remained")
		}
	}
	if e = os.WriteFile(filepath.Join(store.Dir, "identity.json"), []byte("corrupt or foreign user ciphertext"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = other.Profile(); e == nil {
		t.Fatal("corrupt profile accepted")
	}
}

func TestClientRejectsUnsafeTransportAndRedactsErrors(t *testing.T) {
	for _, server := range []string{"http://api.example", "https://user:secret@api.example", "https://api.example?token=secret", "https://api.example/#fragment", "javascript:bad"} {
		if _, e := NewClient(server); e == nil {
			t.Fatal("unsafe server accepted", server)
		}
	}
	var forwarded bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(307)
		_, _ = io.WriteString(w, "private server body with secret")
	}))
	defer server.Close()
	c, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	c.token = "private-access-token"
	e = c.JSON(t.Context(), "POST", "/sensitive", map[string]string{"secret": "private"}, nil, true)
	if e == nil || strings.Contains(e.Error(), "secret") || forwarded {
		t.Fatal("redirect forwarded credential or error exposed body", e)
	}
}

func TestReceiptOutboxSurvivesLostAcknowledgementAndReplaysExactBody(t *testing.T) {
	store := testStore(t)
	j := journal{Attempt: &attempt{ID: "21", Epoch: 3}}
	if e := j.enqueue(store, receipt{Kind: "ACCEPTED"}); e != nil {
		t.Fatal(e)
	}
	var received [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = append(received, b)
		if len(received) == 1 {
			w.WriteHeader(503)
			return
		}
		respond(w, receiptAck{Seq: 1, State: "RUNNING"})
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	f := fence{MachineID: "11", SessionID: "22", Epoch: 5}
	if _, e := j.flush(t.Context(), store, c, f); e == nil {
		t.Fatal("lost acknowledgement treated as success")
	}
	var recovered journal
	if e := store.Read("journal.json", &recovered); e != nil {
		t.Fatal(e)
	}
	if len(recovered.Outbox) != 1 || recovered.Outbox[0].Receipt.Seq != 1 {
		t.Fatal("unacknowledged receipt lost")
	}
	if _, e := recovered.flush(t.Context(), store, c, f); e != nil {
		t.Fatal(e)
	}
	if len(received) != 2 || !bytes.Equal(received[0], received[1]) {
		t.Fatal("retry changed idempotent receipt")
	}
	if len(recovered.Outbox) != 0 || recovered.Attempt.Seq != 1 {
		t.Fatal("acknowledgement did not advance durable outbox")
	}
	if e := store.Read("journal.json", &recovered); e != nil || len(recovered.Outbox) != 0 {
		t.Fatal("outbox acknowledgement not durable", e)
	}
}

func TestInventoryPublishesObservedFactsAndRejectsPartialFailure(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in struct {
			ObservationMode string `json:"observation_mode"`
			Inventory       struct {
				Runtimes []map[string]any `json:"runtimes"`
				Tools    []map[string]any `json:"tools"`
			} `json:"inventory"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ObservationMode != "RUNTIME_VERSIONS" {
			t.Error("version-only observation can erase model catalog")
		}
		if len(in.Inventory.Runtimes) != 1 || len(in.Inventory.Tools) != 1 {
			t.Error("wrong inventory")
		}
		if models, ok := in.Inventory.Runtimes[0]["models"].([]any); !ok || len(models) != 0 {
			t.Error("model catalog invented or missing required array")
		}
		respond(w, true)
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	p := testProfile(server.URL)
	report := inspection.Report{Executables: []inspection.Executable{{Kind: "CODEX", Name: "codex", Available: true, Version: "1", Path: "/codex"}, {Kind: "TOOL", Name: "node", Available: true, Version: "2", Path: "/node"}, {Name: "python", Error: "NOT_FOUND"}}}
	if e := publishInventory(t.Context(), c, p, fence{}, report); e != nil {
		t.Fatal(e)
	}
	report.Executables[0].Error = "TIMEOUT"
	if e := publishInventory(t.Context(), c, p, fence{}, report); e == nil || calls != 1 {
		t.Fatal("partial probe overwrote last successful inventory", e, calls)
	}
}

func TestDaemonJournalsBeforeLaunchAndNeverConvertsRuntimeSuccessToDeliverySuccess(t *testing.T) {
	for _, runtimeKind := range []string{"CODEX", "CLAUDE_CODE"} {
		t.Run(runtimeKind, func(t *testing.T) {
			store := testStore(t)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var mu sync.Mutex
			var kinds []string
			terminal := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/machines/11/discovery":
					respond(w, true)
				case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
					respond(w, []inspectionRequest{})
				case "/machines/11/heartbeat":
					respond(w, true)
				case "/runner/execution-pulls":
					respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: runtimeKind, Model: "test", Workdir: store.Dir, Objective: "Read repository", AuthorizationMode: "READ_ONLY"}}})
				case "/executions/21/receipts":
					var in struct {
						Receipt receipt `json:"receipt"`
					}
					_ = json.NewDecoder(r.Body).Decode(&in)
					mu.Lock()
					kinds = append(kinds, in.Receipt.Kind)
					mu.Unlock()
					state := "RUNNING"
					if in.Receipt.Kind == "LOST" {
						state = "INTERRUPTED"
						defer close(terminal)
					}
					if strings.Contains(in.Receipt.Content, "sensitive-runtime-text") {
						t.Error("runtime text leaked into transport")
					}
					respond(w, receiptAck{Seq: in.Receipt.Seq, State: state})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c, _ := NewClient(server.URL)
			c.token = "test-token"
			c.expires = time.Now().Add(time.Hour)
			j := journal{}
			launches := 0
			run := func(ctx context.Context, plan executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
				launches++
				var saved journal
				if e := store.Read("journal.json", &saved); e != nil || saved.Attempt == nil || !saved.Attempt.Running || saved.Attempt.Seq != 1 || len(saved.Outbox) != 0 {
					t.Error("runtime launched without durable acknowledged attempt", e)
				}
				if e := emit(executor.Observation{Kind: "LOG", Content: "sensitive-runtime-text"}); e != nil {
					return executor.Result{}, e
				}
				return executor.Result{RuntimeCompleted: true, HeadCommit: strings.Repeat("a", 40), ResultState: "RUNTIME_CONFIRMED"}, nil
			}
			done := make(chan error, 1)
			go func() {
				done <- daemonLoop(ctx, store, c, testProfile(server.URL), fence{MachineID: "11", SessionID: "22", Epoch: 1}, &j, run)
			}()
			select {
			case <-terminal:
			case <-ctx.Done():
				t.Fatal("runtime did not reach truthful terminal receipt")
			}
			// The store has one owner. Join the daemon before inspecting its
			// files; an external reader can block Windows atomic replacement.
			if e := <-done; !errors.Is(e, context.DeadlineExceeded) {
				t.Fatal(e)
			}
			var saved journal
			if e := store.Read("journal.json", &saved); e != nil || saved.Attempt != nil || len(saved.Outbox) != 0 {
				t.Fatal("terminal acknowledgement was not saved", e)
			}
			if launches != 1 {
				t.Fatal("unexpected launch count", launches)
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(kinds, ",") != "ACCEPTED,LOG,LOST" {
				t.Fatal("unexpected runtime/delivery semantics", kinds)
			}
			if j.Seen["21"] != 1 {
				t.Fatal("attempt replay fence not retained")
			}
		})
	}
}

func TestDaemonRejectsDuplicateAttemptBeforeAnyRuntimeLaunch(t *testing.T) {
	store := testStore(t)
	j := journal{Seen: map[string]int64{"21": 2}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/machines/11/agent-workspace-pulls" || r.URL.Path == "/runner/merge-pulls" || r.URL.Path == "/runner/verification-pulls" || r.URL.Path == "/machines/11/agent-reset-pulls" || r.URL.Path == "/machines/11/agent-command-pulls" || r.URL.Path == "/machines/11/inspection-pulls" || r.URL.Path == "/machines/11/credential-deliveries" {
			respond(w, []inspectionRequest{})
		} else if r.URL.Path == "/runner/execution-pulls" {
			respond(w, []command{{ID: "21", MachineID: "11", Epoch: 2, State: "CREATED"}})
		} else {
			respond(w, true)
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	c.token = "test"
	c.expires = time.Now().Add(time.Hour)
	e := daemonLoop(t.Context(), store, c, testProfile(server.URL), fence{}, &j, func(context.Context, executor.Plan, func(executor.Observation) error) (executor.Result, error) {
		t.Error("duplicate launched")
		return executor.Result{}, nil
	})
	if e == nil || !strings.Contains(e.Error(), "duplicate") {
		t.Fatal(e)
	}
}

func TestDaemonTransportFailureStopsRuntimeAndRetainsUnacknowledgedReceipt(t *testing.T) {
	store := testStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/discovery":
			respond(w, true)
		case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
			respond(w, []inspectionRequest{})
		case "/machines/11/heartbeat":
			respond(w, true)
		case "/runner/execution-pulls":
			respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Workdir: store.Dir, Objective: "Observe", AuthorizationMode: "READ_ONLY"}}})
		case "/executions/21/receipts":
			var in struct {
				Receipt receipt `json:"receipt"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Receipt.Kind == "ACCEPTED" {
				respond(w, receiptAck{Seq: in.Receipt.Seq, State: "RUNNING"})
			} else {
				w.WriteHeader(503)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	c.token = "test"
	c.expires = time.Now().Add(time.Hour)
	stopped := false
	j := journal{}
	e := daemonLoop(t.Context(), store, c, testProfile(server.URL), fence{}, &j, func(ctx context.Context, _ executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
		_ = emit(executor.Observation{Kind: "LOG"})
		<-ctx.Done()
		stopped = true
		return executor.Result{}, executor.ErrUnknownResult
	})
	var api *APIError
	if !errors.As(e, &api) || api.Status != 503 || !stopped {
		t.Fatal("transport failure did not cancel and join executor", e, stopped)
	}
	var saved journal
	if e = store.Read("journal.json", &saved); e != nil {
		t.Fatal(e)
	}
	if saved.Attempt == nil || !saved.Attempt.Running || len(saved.Outbox) != 1 || saved.Outbox[0].Receipt.Kind != "LOG" || saved.Outbox[0].Receipt.Seq != 2 {
		t.Fatal("unacknowledged runtime facts lost", saved)
	}
}

func TestReceiptWrongAcknowledgementCannotDiscardDurableOutbox(t *testing.T) {
	store := testStore(t)
	j := journal{Attempt: &attempt{ID: "21", Epoch: 1}}
	if e := j.enqueue(store, receipt{Kind: "ACCEPTED"}); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { respond(w, receiptAck{Seq: 99, State: "RUNNING"}) }))
	defer server.Close()
	c, _ := NewClient(server.URL)
	if _, e := j.flush(t.Context(), store, c, fence{}); e == nil {
		t.Fatal("wrong acknowledgement accepted")
	}
	var saved journal
	if e := store.Read("journal.json", &saved); e != nil || len(saved.Outbox) != 1 {
		t.Fatal("receipt lost after invalid acknowledgement", e)
	}
}

func TestInspectionReportsOnlySupportedObservedFacts(t *testing.T) {
	for _, kind := range []string{"RUNTIMES", "MODELS", "CAPABILITIES", "FUTURE_KIND"} {
		t.Run(kind, func(t *testing.T) {
			var submitted map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PUT" || r.URL.Path != "/machines/11/inspections/31/result" {
					t.Error("wrong inspection route")
				}
				_ = json.NewDecoder(r.Body).Decode(&submitted)
				respond(w, true)
			}))
			defer server.Close()
			c, _ := NewClient(server.URL)
			calls := 0
			err := completeInspection(t.Context(), c, testProfile(server.URL), fence{SessionID: "22", Epoch: 3}, inspectionRequest{ID: "31", Kind: kind}, func(context.Context) inspection.Report {
				calls++
				return inspection.Report{Executables: []inspection.Executable{{Kind: "CODEX", Available: true, Version: "1", Path: "/codex"}}}
			})
			if err != nil {
				t.Fatal(err)
			}
			if submitted["session_id"] != "22" || submitted["conn_epoch"] != float64(3) {
				t.Fatal("missing current session fence")
			}
			if kind == "RUNTIMES" {
				if calls != 1 || submitted["succeeded"] != true || submitted["inventory"] == nil {
					t.Fatal("observed runtime result missing", submitted)
				}
			} else if kind == "MODELS" || kind == "CAPABILITIES" {
				if calls != 0 || submitted["succeeded"] != true || submitted["inventory"] != nil {
					t.Fatal("discovery must use separate local adapters", submitted)
				}
				facts, ok := submitted["discovery"].([]any)
				if !ok || len(facts) != 2 {
					t.Fatal("per-runtime discovery facts missing", submitted)
				}
			} else {
				if calls != 0 || submitted["succeeded"] != false || submitted["reason_code"] != "UNSUPPORTED_INSPECTION_KIND" || submitted["inventory"] != nil {
					t.Fatal("unknown kind must fail", submitted)
				}
			}
		})
	}
}

func TestInspectionFailureOmitsPartialInventoryAndRawErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var submitted map[string]any
		_ = json.Unmarshal(body, &submitted)
		if submitted["succeeded"] != false || submitted["reason_code"] != "INSPECTION_FAILED" || submitted["inventory"] != nil || bytes.Contains(body, []byte("PRIVATE_ERROR")) {
			t.Error("partial or sensitive failed inspection submitted", submitted)
		}
		respond(w, true)
	}))
	defer server.Close()
	c, _ := NewClient(server.URL)
	err := completeInspection(t.Context(), c, testProfile(server.URL), fence{}, inspectionRequest{ID: "31", Kind: "RUNTIMES"}, func(context.Context) inspection.Report {
		return inspection.Report{Executables: []inspection.Executable{{Kind: "CODEX", Error: "PRIVATE_ERROR"}}}
	})
	if err != nil {
		t.Fatal(err)
	}
}
