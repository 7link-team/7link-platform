package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestDaemonCancellationRequiresConfirmedTreeShutdown(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			store := testStore(t)
			terminal := make(chan receipt, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
					respond(w, []inspectionRequest{})
				case "/runner/execution-pulls":
					respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Workdir: store.Dir, Objective: "Observe", AuthorizationMode: "READ_ONLY"}}})
				case "/executions/21/receipts":
					var in struct {
						Receipt receipt `json:"receipt"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
					}
					if in.Receipt.Kind != "ACCEPTED" && in.Receipt.Kind != "LOG" && in.Receipt.Kind != "HEARTBEAT" {
						terminal <- in.Receipt
					}
					respond(w, receiptAck{Seq: in.Receipt.Seq, State: "RUNNING", Cancel: in.Receipt.Kind == "LOG"})
				default:
					respond(w, true)
				}
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "test-token", time.Now().Add(time.Hour)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				j := journal{}
				done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(runCtx context.Context, _ executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
					_ = emit(executor.Observation{Kind: "LOG"})
					<-runCtx.Done()
					return executor.Result{CancellationConfirmed: confirmed}, executor.ErrUnknownResult
				})
			}()
			select {
			case received := <-terminal:
				want := "LOST"
				if confirmed {
					want = "CANCELLED"
				}
				if received.Kind != want || received.Upload != nil || received.HeadCommit != "" {
					t.Errorf("untruthful cancellation/evidence: %+v", received)
				}
			case <-time.After(8 * time.Second):
				t.Error("cancellation did not produce terminal receipt")
			}
			cancel()
			<-done
		})
	}
}

func TestDaemonHandoverTransmitsOnlyActuallyCompletedDocumentWithoutClaim(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown_runtime", true: "completed_document"}[completed], func(t *testing.T) {
			store := testStore(t)
			terminal := make(chan receipt, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/runner/execution-pulls":
					respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Objective: "Write handover", ExecutorPrincipalID: "7", AuthorizationMode: "READ_ONLY", Handover: &executor.HandoverPlan{RetirementID: "31", AgentPrincipalID: "7"}}}})
				case "/executions/21/receipts":
					var body struct {
						Receipt receipt `json:"receipt"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					state := "RUNNING"
					if body.Receipt.Kind == "COMPLETED" || body.Receipt.Kind == "LOST" || body.Receipt.Kind == "FAILED" {
						terminal <- body.Receipt
						state = "SUCCEEDED"
					}
					respond(w, receiptAck{Seq: body.Receipt.Seq, State: state})
				default:
					respond(w, []inspectionRequest{})
				}
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "fixture-token", time.Now().Add(time.Hour)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				j := journal{}
				done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(_ context.Context, p executor.Plan, _ func(executor.Observation) error) (executor.Result, error) {
					if p.Handover == nil || p.BaseCommit != "" || p.Workdir != "" {
						t.Error("handover acquired fabricated Git context")
					}
					return executor.Result{RuntimeCompleted: completed, HandoverDocument: "Actual fixture runtime document"}, nil
				})
			}()
			select {
			case got := <-terminal:
				if got.HeadCommit != "" || got.Upload != nil || got.EvidenceKind != "" {
					t.Errorf("handover fabricated evidence: %+v", got)
				}
				if completed {
					if got.Kind != "COMPLETED" || got.Content != "Actual fixture runtime document" {
						t.Errorf("document lost: %+v", got)
					}
				} else if got.Kind == "COMPLETED" {
					t.Error("unconfirmed runtime document accepted")
				}
			case <-time.After(8 * time.Second):
				t.Error("no terminal handover receipt")
			}
			cancel()
			<-done
		})
	}
}

func TestDaemonFullAutoUsesServerEnvelopeGrantBeforePreflight(t *testing.T) {
	for _, test := range []struct {
		name, grant     string
		forgedPlanGrant bool
	}{
		{name: "approved", grant: "123"},
		{name: "missing"},
		{name: "forged_plan", forgedPlanGrant: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t)
			launched := make(chan executor.Plan, 1)
			failed := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
					respond(w, []inspectionRequest{})
				case "/runner/execution-pulls":
					plan := map[string]any{"runtime": "CODEX", "model": "fixture", "workdir": store.Dir, "objective": "Approved unattended work", "authorization_mode": "FULL_AUTO"}
					if test.forgedPlanGrant {
						plan["full_auto_grant_id"] = "999"
					}
					respond(w, []map[string]any{{"id": "21", "machine_id": "11", "dispatch_epoch": 1, "state": "CREATED", "full_auto_grant_id": test.grant, "plan": plan}})
				case "/executions/21/receipts":
					var body struct {
						Receipt receipt `json:"receipt"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					state := "RUNNING"
					if body.Receipt.Kind == "FAILED" {
						state = "FAILED"
						select {
						case failed <- struct{}{}:
						default:
						}
					}
					respond(w, receiptAck{Seq: body.Receipt.Seq, State: state})
				default:
					respond(w, true)
				}
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "test-token", time.Now().Add(time.Hour)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				j := journal{}
				done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(runCtx context.Context, plan executor.Plan, _ func(executor.Observation) error) (executor.Result, error) {
					launched <- plan
					<-runCtx.Done()
					return executor.Result{}, executor.ErrUnknownResult
				})
			}()
			select {
			case plan := <-launched:
				if test.grant == "" || plan.FullAutoGrantID != test.grant {
					t.Errorf("runtime launched without exact envelope grant: %+v", plan)
				}
			case <-failed:
				if test.grant != "" {
					t.Error("approved FULL_AUTO command failed preflight")
				}
			case <-time.After(8 * time.Second):
				t.Error("dispatch produced neither runtime launch nor failure receipt")
			}
			cancel()
			<-done
			if test.grant == "" {
				select {
				case <-launched:
					t.Error("unapproved FULL_AUTO command reached runtime")
				default:
				}
			}
		})
	}
}

func TestWorkspaceEvidenceUploadsActualBytesBeforeClaimAndHonorsCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "submit", true: "cancel"}[cancel], func(t *testing.T) {
			store := testStore(t)
			j := journal{Attempt: &attempt{ID: "21", Epoch: 2, Seq: 1, Running: true}}
			patch := []byte("diff --git a/result.txt b/result.txt\n+actual change\n")
			head := strings.Repeat("b", 40)
			var received []receipt
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Receipt receipt `json:"receipt"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var saved journal
				if err := store.Read("journal.json", &saved); err != nil || len(saved.Outbox) != 1 || saved.Outbox[0].Receipt.Seq != body.Receipt.Seq {
					t.Error("evidence transmitted before durable journal", err)
				}
				received = append(received, body.Receipt)
				respond(w, receiptAck{Seq: body.Receipt.Seq, State: "RUNNING", Cancel: cancel})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "test-token", time.Now().Add(time.Hour)
			requested, err := submitWorkspaceEvidence(t.Context(), store, client, fence{MachineID: "11", SessionID: "22", Epoch: 2}, &j, executor.Result{Diff: patch, HeadCommit: head})
			if err != nil || requested != cancel || len(received) == 0 {
				t.Fatal(requested, err, received)
			}
			file := received[0]
			if file.Kind != "EVIDENCE" || file.EvidenceKind != "FILE" || file.Upload == nil || !bytes.Equal(file.Upload.Data, patch) || file.Upload.Coverage != "NOT_APPLICABLE" || file.Epoch != 2 || file.Seq != 2 {
				t.Fatal("artifact content or authority changed", file)
			}
			if cancel && len(received) != 1 {
				t.Fatal("continued evidence submission after cancellation", received)
			}
			if !cancel && (len(received) != 2 || received[1].EvidenceKind != "COMMIT" || received[1].Content != head || received[1].Seq != 3) {
				t.Fatal("commit evidence missing", received)
			}
		})
	}
}

func TestDaemonJournalsModelCitationOutputBeforeSendingEvidence(t *testing.T) {
	store := testStore(t)
	cited := make(chan receipt, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
			respond(w, []inspectionRequest{})
		case "/runner/execution-pulls":
			respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Workdir: store.Dir, Objective: "Observe", AuthorizationMode: "READ_ONLY", Knowledge: []executor.KnowledgeItem{{EntryID: "90", Version: 1, Label: "[K1]", Title: "Policy", Body: "Keep receipts", ContentHash: "32cfa3addd7f3ab8e3ca43922f3343751b4f49af14b1b69fc8ed15cf390f1706"}}}}})
		case "/executions/21/receipts":
			var body struct {
				Receipt receipt `json:"receipt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Receipt.Kind == "EVIDENCE" {
				var saved journal
				if err := store.Read("journal.json", &saved); err != nil {
					t.Error(err)
				}
				found := false
				for _, queued := range saved.Outbox {
					if queued.Receipt.Seq == body.Receipt.Seq && queued.Receipt.Content == body.Receipt.Content {
						found = true
					}
				}
				if !found {
					t.Error("citation evidence sent before durable journal")
				}
				cited <- body.Receipt
			}
			respond(w, receiptAck{Seq: body.Receipt.Seq, State: "RUNNING"})
		default:
			respond(w, true)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	client.token, client.expires = "test-token", time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		j := journal{}
		done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(runCtx context.Context, _ executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
			if err := emit(executor.Observation{Kind: "RUNTIME_EVENT", Content: "Tool echoed [K1]"}); err != nil {
				return executor.Result{}, err
			}
			if err := emit(executor.Observation{Kind: "LOG", Content: "Actual model answer cites [K1]"}); err != nil {
				return executor.Result{}, err
			}
			<-runCtx.Done()
			return executor.Result{}, executor.ErrUnknownResult
		})
	}()
	select {
	case got := <-cited:
		if got.EvidenceKind != "OBSERVATION" || got.Content != "Actual model answer cites [K1]" {
			t.Errorf("incorrect evidence: %+v", got)
		}
	case <-time.After(8 * time.Second):
		t.Error("actual citation output not delivered")
	}
	cancel()
	<-done
}
