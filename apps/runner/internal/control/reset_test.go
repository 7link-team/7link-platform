package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentResetReceiptIsDurableRetryableAndTruthful(t *testing.T) {
	store := testStore(t)
	cmd := agentReset{ID: "21", AgentPrincipalID: "31", MachineID: "11", Kind: "FULL", State: "RUNNING", SessionEpoch: 2, WorkspaceEpoch: 2, ExpiresAt: time.Now().Add(time.Minute).UTC()}
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/agent-reset-pulls":
			respond(w, []agentReset{cmd})
		case "/machines/11/agent-resets/21/result":
			puts++
			var body resetReceipt
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.State != "FAILED" || body.Reason != "UNSUPPORTED_AGENT_RUNTIME" || body.SessionID != "41" || body.Epoch != 3 {
				t.Errorf("false or unfenced result: %+v", body)
			}
			var durable resetReceipt
			if err := store.Read("agent-reset-receipt.json", &durable); err != nil {
				t.Error(err)
			}
			if durable.State != "FAILED" || durable.Command.ID != cmd.ID {
				t.Error("receipt not durable before transmission")
			}
			if puts == 1 {
				w.WriteHeader(503)
				return
			}
			ack := cmd
			ack.State = "FAILED"
			respond(w, ack)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := testProfile(server.URL)
	f := fence{MachineID: "11", SessionID: "41", Epoch: 3}
	if err = processAgentReset(t.Context(), store, c, p, f); err == nil {
		t.Fatal("lost receipt response must fail")
	}
	if err = processAgentReset(t.Context(), store, c, p, f); err != nil {
		t.Fatal(err)
	}
	if puts != 2 {
		t.Fatalf("receipt retry count %d", puts)
	}
	cmd.SessionEpoch++
	if err = processAgentReset(t.Context(), store, c, p, f); err == nil {
		t.Fatal("same ID with changed command accepted")
	}
}

func TestAgentResetRejectsMalformedCommandsAndAcknowledgements(t *testing.T) {
	for _, scenario := range []string{"empty", "batch", "id", "principal", "machine", "kind", "state", "session_epoch", "workspace_epoch", "expired", "ack", "store"} {
		t.Run(scenario, func(t *testing.T) {
			store := testStore(t)
			cmd := agentReset{ID: "21", AgentPrincipalID: "31", MachineID: "11", Kind: "SESSION", State: "RUNNING", SessionEpoch: 2, WorkspaceEpoch: 1, ExpiresAt: time.Now().Add(time.Minute)}
			switch scenario {
			case "id":
				cmd.ID = "../escape"
			case "principal":
				cmd.AgentPrincipalID = "0"
			case "machine":
				cmd.MachineID = "12"
			case "kind":
				cmd.Kind = "CLEAR_ALL"
			case "state":
				cmd.State = "DONE"
			case "session_epoch":
				cmd.SessionEpoch = 0
			case "workspace_epoch":
				cmd.WorkspaceEpoch = 0
			case "expired":
				cmd.ExpiresAt = time.Now().Add(-time.Minute)
			case "store":
				store.Dir = t.TempDir() + "/missing"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					commands := []agentReset{cmd}
					if scenario == "batch" {
						commands = append(commands, cmd)
					}
					if scenario == "empty" {
						commands = []agentReset{}
					}
					respond(w, commands)
					return
				}
				ack := cmd
				ack.State = "DONE"
				respond(w, ack)
			}))
			defer server.Close()
			c, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			err = processAgentReset(t.Context(), store, c, testProfile(server.URL), fence{MachineID: "11", SessionID: "41", Epoch: 3})
			if scenario == "empty" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("invalid reset accepted")
			}
		})
	}
}
