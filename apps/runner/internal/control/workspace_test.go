package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentWorkspaceExpiredResultRequiresCurrentTransportFence(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			store := testStore(t)
			pulls, results := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/machines/11/agent-workspace-pulls":
					pulls++
					var body struct {
						SessionID string `json:"session_id"`
						ConnEpoch int64  `json:"conn_epoch"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SessionID != "41" || body.ConnEpoch != 3 || r.Header.Get("Authorization") != "Bearer machine-token" {
						t.Error("transport fence or authentication changed")
					}
					if pulls == 1 {
						respond(w, []workspaceCommand{{ID: "71", AgentPrincipalID: "21", MachineID: "11", SessionID: "41", ConnEpoch: 3, ConfigRevision: 1, Kind: "LIST", ExpiresAt: time.Now().Add(time.Minute)}})
					} else if status == http.StatusOK {
						respond(w, []workspaceCommand{})
					} else {
						w.WriteHeader(status)
					}
				case "/machines/11/agent-workspace-reads/71/result":
					results++
					// The human read disappeared while the machine was reading.
					w.WriteHeader(http.StatusConflict)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client.token = "machine-token"
			err = processAgentWorkspace(t.Context(), store, client, testProfile(server.URL), fence{MachineID: "11", SessionID: "41", Epoch: 3})
			if status == http.StatusOK {
				if err != nil {
					t.Fatalf("obsolete read must not stop the execution: %v", err)
				}
			} else {
				var rejected *APIError
				if !errors.As(err, &rejected) || rejected.Status != status {
					t.Fatalf("transport rejection must remain fatal: %v", err)
				}
			}
			if pulls != 2 || results != 1 {
				t.Fatalf("wrong stale-result flow: pulls=%d results=%d", pulls, results)
			}
		})
	}
}

func TestAgentWorkspaceMachineTransportUsesExactFenceAndRealContent(t *testing.T) {
	store := testStore(t)
	if e := store.Write("agent-credential-21.json", privateAgentCredential{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 1}); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(store.Dir, "agent-private-21")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "note"), []byte("observed"), 0600); e != nil {
		t.Fatal(e)
	}
	reports := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/agent-workspace-pulls":
			respond(w, []workspaceCommand{{ID: "71", AgentPrincipalID: "21", MachineID: "11", SessionID: "41", ConnEpoch: 3, ConfigRevision: 1, Path: "note", Kind: "CONTENT", ExpiresAt: time.Now().Add(time.Minute)}})
		case "/machines/11/agent-workspace-reads/71/result":
			var body struct {
				SessionID string `json:"session_id"`
				ConnEpoch int64  `json:"conn_epoch"`
				Content   string `json:"content"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if body.SessionID != "41" || body.ConnEpoch != 3 || body.Content != "observed" {
				t.Errorf("wrong real result: %+v", body)
			}
			reports++
			respond(w, true)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	if e = processAgentWorkspace(t.Context(), store, client, testProfile(server.URL), fence{MachineID: "11", SessionID: "41", Epoch: 3}); e != nil {
		t.Fatal(e)
	}
	if reports != 1 {
		t.Fatal("actual result not returned")
	}
	entries, e := os.ReadDir(store.Dir)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() == "workspace-result.json" {
			t.Fatal("file content was persisted")
		}
	}
}

func TestAgentWorkspaceReadsRealPrivateFilesAndRejectsBoundaries(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	if e := store.Write("agent-credential-21.json", privateAgentCredential{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 2}); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(store.Dir, "agent-private-21")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "note.txt"), []byte("private note"), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := workspaceCommand{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 2, Kind: "LIST", ExpiresAt: time.Now().Add(time.Minute)}
	list := readAgentWorkspace(store, cmd)
	if list.Reason != "" || len(list.Items) != 1 || list.Items[0].Name != "note.txt" {
		t.Fatalf("actual list: %+v", list)
	}
	cmd.Kind, cmd.Path = "CONTENT", "note.txt"
	got := readAgentWorkspace(store, cmd)
	if got.Reason != "" || got.Content != "private note" {
		t.Fatalf("actual content: %+v", got)
	}
	for _, p := range []string{"../agent-credential-21.json", "/absolute", "C:/host", "a\\b", "a/../note.txt", "missing"} {
		cmd.Path = p
		got = readAgentWorkspace(store, cmd)
		if got.Reason == "" || got.Content != "" {
			t.Fatalf("accepted %q: %+v", p, got)
		}
	}
	cmd.Path = "note.txt"
	cmd.ConfigRevision = 3
	if got = readAgentWorkspace(store, cmd); got.Reason != "UNAVAILABLE" {
		t.Fatalf("stale config: %+v", got)
	}
	cmd.ConfigRevision = 2
	cmd.ExpiresAt = time.Now().Add(-time.Second)
	if got = readAgentWorkspace(store, cmd); got.Reason == "" {
		t.Fatal("expired read accepted")
	}
}
func TestAgentWorkspaceSizeBinaryAndSymlinkContainment(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	if e := store.Write("agent-credential-21.json", privateAgentCredential{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 1}); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(store.Dir, "agent-private-21")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	cmd := workspaceCommand{AgentPrincipalID: "21", MachineID: "11", ConfigRevision: 1, Kind: "CONTENT", Path: "large", ExpiresAt: time.Now().Add(time.Minute)}
	if e := os.WriteFile(filepath.Join(root, "large"), make([]byte, 1048577), 0600); e != nil {
		t.Fatal(e)
	}
	if got := readAgentWorkspace(store, cmd); got.Reason != "TOO_LARGE" {
		t.Fatalf("large: %+v", got)
	}
	cmd.Path = "binary"
	if e := os.WriteFile(filepath.Join(root, "binary"), []byte{255, 0}, 0600); e != nil {
		t.Fatal(e)
	}
	if got := readAgentWorkspace(store, cmd); got.Reason != "INVALID_PATH" {
		t.Fatalf("binary: %+v", got)
	}
	outside := filepath.Join(store.Dir, "secret")
	if e := os.WriteFile(outside, []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e == nil {
		cmd.Path = "escape"
		if got := readAgentWorkspace(store, cmd); got.Reason != "INVALID_PATH" || got.Content != "" {
			t.Fatalf("escaped: %+v", got)
		}
	} else {
		t.Log("OS does not permit symlink creation; path validation and Root containment still exercised")
	}
}
