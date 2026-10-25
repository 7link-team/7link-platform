package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentCredentialStoredBeforeAcknowledgementAndLostACKReplaysReceipt(t *testing.T) {
	store := testStore(t)
	cmd := agentMachineCommand{ID: "71", AgentPrincipalID: "21", MachineID: "11", CredentialID: "31", ClientID: "31", Secret: "agent-private-secret", ConfigRevision: 2, Kind: "CREDENTIAL", State: "RUNNING", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	acknowledgements := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/agent-command-pulls":
			respond(w, []agentMachineCommand{cmd})
		case "/machines/11/agent-commands/71/result":
			acknowledgements++
			var credential privateAgentCredential
			if err := store.Read("agent-credential-21.json", &credential); err != nil {
				t.Error(err)
			}
			want := cmd.Secret
			if acknowledgements > 1 {
				want = "later-local-value"
			}
			if credential.Secret != want {
				t.Error("credential action repeated or was not durable before ACK")
			}
			var receipt agentMachineReceipt
			if err := store.Read("agent-command-receipt-71.json", &receipt); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(receipt)
			if strings.Contains(string(encoded), cmd.Secret) || receipt.State != "DONE" {
				t.Error("receipt contains plaintext or false completion")
			}
			if acknowledgements == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			ack := cmd
			ack.State = "DONE"
			ack.Secret = ""
			respond(w, ack)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := testProfile(server.URL)
	f := fence{SessionID: "51", Epoch: 1}
	if err = processAgentMachineCommands(t.Context(), store, c, p, f, nil); err == nil {
		t.Fatal("lost ACK was accepted")
	}
	if err = store.Write("agent-credential-21.json", privateAgentCredential{Secret: "later-local-value"}); err != nil {
		t.Fatal(err)
	}
	if err = processAgentMachineCommands(t.Context(), store, c, p, f, nil); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 2 {
		t.Fatal("receipt was not retried")
	}
	var receipt agentMachineReceipt
	if err = store.Read("agent-command-receipt-71.json", &receipt); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("acknowledged receipt retained", err)
	}
}

func TestPrivateAgentCleanupPreservesProjectFilesAndRejectsTraversal(t *testing.T) {
	store := testStore(t)
	private := filepath.Join(store.Dir, "agent-private-21")
	project := filepath.Join(store.Dir, "project-workdir")
	for _, dir := range []string{private, project} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Write("agent-credential-21.json", privateAgentCredential{Secret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := cleanupPrivateAgent(store, "21"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{private, filepath.Join(store.Dir, "agent-credential-21.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("private state retained", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "sentinel")); err != nil {
		t.Fatal("project data removed", err)
	}
	for _, id := range []string{"../project-workdir", "21/../../project-workdir", "0", "021"} {
		if err := cleanupPrivateAgent(store, id); err == nil {
			t.Fatal("invalid identity accepted", id)
		}
	}
	if err := os.Symlink(project, private); err != nil {
		t.Log("symlink unavailable; numeric traversal and boundary checks verified", err)
		return
	}
	if err := cleanupPrivateAgent(store, "21"); err == nil {
		t.Fatal("symlink target accepted")
	}
	if _, err := os.Stat(filepath.Join(project, "sentinel")); err != nil {
		t.Fatal("symlink target changed", err)
	}
}

func TestAgentMachineCommandRejectsMalformedAndMismatchedAcknowledgements(t *testing.T) {
	for _, kind := range []string{"foreign-machine", "expired", "secret-on-cleanup", "ack-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			store := testStore(t)
			cmd := agentMachineCommand{ID: "71", AgentPrincipalID: "21", MachineID: "11", CredentialID: "31", ClientID: "31", Secret: "private", ConfigRevision: 2, Kind: "CREDENTIAL", State: "RUNNING", ExpiresAt: time.Now().UTC().Add(time.Hour)}
			switch kind {
			case "foreign-machine":
				cmd.MachineID = "12"
			case "expired":
				cmd.ExpiresAt = time.Now().Add(-time.Hour)
			case "secret-on-cleanup":
				cmd.Kind = "CLEANUP"
			}
			acks := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "agent-command-pulls") {
					respond(w, []agentMachineCommand{cmd})
					return
				}
				acks++
				ack := cmd
				ack.ID = "72"
				ack.State = "DONE"
				respond(w, ack)
			}))
			defer server.Close()
			c, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if err = processAgentMachineCommands(t.Context(), store, c, testProfile(server.URL), fence{SessionID: "51", Epoch: 1}, nil); err == nil {
				t.Fatal("invalid command/ACK accepted")
			}
			if kind != "ack-mismatch" && acks != 0 {
				t.Fatal("invalid command acknowledged")
			}
			if kind == "ack-mismatch" {
				var receipt agentMachineReceipt
				if err = store.Read("agent-command-receipt-71.json", &receipt); err != nil {
					t.Fatal("unconfirmed receipt discarded", err)
				}
			}
		})
	}
}

func TestReboundWorkerConsumesExactProtectedCredentialAndChecksAuthenticatedPrincipal(t *testing.T) {
	for _, fault := range []string{"none", "config", "zero_revision", "binding_principal", "binding_machine", "binding_credential", "empty_secret", "principal", "missing"} {
		t.Run(fault, func(t *testing.T) {
			store := testStore(t)
			tokens := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/token":
					tokens++
					_ = r.ParseForm()
					if r.Form.Get("client_id") != "31" || r.Form.Get("client_secret") != "private-agent-secret" {
						t.Error("host did not consume bound Agent credential")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "agent-token", "token_type": "Bearer", "expires_in": 3600})
				case "/me":
					if r.Header.Get("Authorization") != "Bearer agent-token" {
						t.Error("host used machine token for Agent")
					}
					principal := "21"
					if fault == "principal" {
						principal = "22"
					}
					respond(w, map[string]string{"subject": principal})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			c.store = store
			credential := privateAgentCredential{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 2, ClientID: "31", Secret: "private-agent-secret"}
			switch fault {
			case "config":
				credential.ConfigRevision = 3
			case "zero_revision":
				credential.ConfigRevision = 0
			case "binding_principal":
				credential.AgentPrincipalID = "22"
			case "binding_machine":
				credential.MachineID = "12"
			case "binding_credential":
				credential.ClientID = "32"
			case "empty_secret":
				credential.Secret = ""
			}
			if fault != "missing" {
				if err = store.Write("agent-credential-21.json", credential); err != nil {
					t.Fatal(err)
				}
			}
			client, identity, err := authenticateAgentWorker(t.Context(), c, testProfile(server.URL), workerProfile{PrincipalID: "21", Runtime: "CODEX", Model: "gpt-model", CredentialID: "31", ConfigRevision: 2})
			if fault == "none" {
				if err != nil || client == nil || identity.ClientID != "31" || tokens != 1 {
					t.Fatal("valid Agent credential not consumed", err, tokens)
				}
			} else {
				if err == nil || client != nil {
					t.Fatal("mismatched Agent credential accepted")
				}
				if fault != "principal" && tokens != 0 {
					t.Fatal("unbound credential transmitted")
				}
			}
		})
	}
}

func TestAgentCredentialRevisionAdvanceRequiresCurrentSelfConfiguration(t *testing.T) {
	for _, fault := range []string{"none", "revision", "principal", "runtime", "model", "forbidden"} {
		t.Run(fault, func(t *testing.T) {
			store := testStore(t)
			confirmations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/token":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "agent-token", "token_type": "Bearer", "expires_in": 3600})
				case "/me":
					respond(w, map[string]string{"subject": "21"})
				case "/tenants/7/agents/21/runtime-config":
					confirmations++
					if r.Header.Get("Authorization") != "Bearer agent-token" {
						t.Error("SELF confirmation used wrong identity")
					}
					if fault == "forbidden" {
						w.WriteHeader(403)
						return
					}
					current := map[string]any{"principal_id": "21", "runtime": "CODEX", "model": "gpt-model", "config_revision": 2}
					switch fault {
					case "revision":
						current["config_revision"] = 1
					case "principal":
						current["principal_id"] = "22"
					case "runtime":
						current["runtime"] = "CLAUDE_CODE"
					case "model":
						current["model"] = "other"
					}
					respond(w, current)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			c.store = store
			if err = store.Write("agent-credential-21.json", privateAgentCredential{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 1, ClientID: "31", Secret: "private-agent-secret"}); err != nil {
				t.Fatal(err)
			}
			client, _, err := authenticateAgentWorker(t.Context(), c, testProfile(server.URL), workerProfile{PrincipalID: "21", Runtime: "CODEX", Model: "gpt-model", CredentialID: "31", ConfigRevision: 2})
			if confirmations != 1 {
				t.Fatal("missing SELF confirmation", confirmations)
			}
			if fault == "none" {
				if err != nil || client == nil {
					t.Fatal("authorized revision advance rejected", err)
				}
			} else if err == nil || client != nil {
				t.Fatal("unconfirmed revision accepted")
			}
		})
	}
}
