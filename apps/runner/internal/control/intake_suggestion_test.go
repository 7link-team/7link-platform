package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntakeConsumerPersistsActualModelOutputBeforeRetryAndNeverForgesIdentity(t *testing.T) {
	store := testStore(t)
	calls, posts := 0, 0
	var bodies [][]byte
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entities/3/intake-suggestions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer actual-agent-token" {
			t.Error("invalid Agent transport", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		posts++
		if posts == 1 {
			w.WriteHeader(503)
			return
		}
		respond(w, map[string]any{"id": "81", "state": "SUGGESTED"})
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.token = "actual-agent-token"
	text := "Real immutable client source"
	hash := sha256.Sum256([]byte(text))
	source := intakeSource{ID: "50", ProjectID: "3", Version: 2, Title: "Client request", Content: &text, ContentHash: hex.EncodeToString(hash[:])}
	model := func(_ context.Context, runtime, model, prompt string) ([]byte, error) {
		calls++
		if runtime != "CODEX" || model != "configured-model" || !strings.Contains(prompt, text) {
			t.Fatal("model input not pinned to real source/config")
		}
		return []byte(`{"assessment":{"summary":"CSV","requirement_draft":"Export selected rows","criteria":["Exact rows"],"risks":[],"questions":[],"size":"S"},"recommendation":"NEW_REQUIREMENT","reason":"Actual requested export"}`), nil
	}
	profile := workerProfile{PrincipalID: "7", Runtime: "CODEX", Model: "configured-model"}
	if err = consumeIntakeSource(t.Context(), store, client, profile, source, model); err == nil {
		t.Fatal("503 claimed success")
	}
	var saved intakeAttempt
	if err = store.Read("intake-suggestion-7-50-2.json", &saved); err != nil || saved.State != "READY" {
		t.Fatal(saved, err)
	}
	if err = consumeIntakeSource(t.Context(), store, client, profile, source, model); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || posts != 2 || string(bodies[0]) != string(bodies[1]) || keys[0] != keys[1] || keys[0] == "" {
		t.Fatal("logical retry changed actual model output/key", calls, posts, keys)
	}
	var body map[string]any
	if json.Unmarshal(bodies[1], &body) != nil || body["intake_id"] != "50" || body["intake_version"] != float64(2) || body["actor_id"] != nil || body["state"] != nil {
		t.Fatal(body)
	}
	if err = consumeIntakeSource(t.Context(), store, client, profile, source, model); err != nil || calls != 1 || posts != 2 {
		t.Fatal("completed suggestion replayed runtime", err, calls, posts)
	}
}
func TestIntakeConsumerUnknownRuntimeNeverPostsOrAutomaticallyReruns(t *testing.T) {
	for _, scenario := range []string{"runtime_error", "forged_identity", "invalid_json", "source_hash"} {
		t.Run(scenario, func(t *testing.T) {
			store := testStore(t)
			calls, posts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				respond(w, map[string]any{"id": "81", "state": "SUGGESTED"})
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			text := "Original source"
			hash := sha256.Sum256([]byte(text))
			source := intakeSource{ID: "50", ProjectID: "3", Version: 1, Content: &text, ContentHash: hex.EncodeToString(hash[:])}
			if scenario == "source_hash" {
				source.ContentHash = "invented"
			}
			model := func(context.Context, string, string, string) ([]byte, error) {
				calls++
				switch scenario {
				case "runtime_error":
					return nil, errors.New("runtime outcome unknown")
				case "forged_identity":
					return []byte(`{"assessment":{},"recommendation":"NEW_REQUIREMENT","reason":"x","actor_id":"2"}`), nil
				default:
					return []byte("invalid"), nil
				}
			}
			profile := workerProfile{PrincipalID: "7", Runtime: "CODEX", Model: "configured"}
			if err := consumeIntakeSource(t.Context(), store, client, profile, source, model); err == nil {
				t.Fatal("invalid runtime accepted")
			}
			_ = consumeIntakeSource(t.Context(), store, client, profile, source, model)
			if posts != 0 || calls > 1 {
				t.Fatal("fabricated suggestion or automatic unknown-turn replay", posts, calls)
			}
		})
	}
}

func TestAgentConsumerReadsMountedManifestInboxAndCurrentSource(t *testing.T) {
	store := testStore(t)
	text := "Actual received API source"
	hash := sha256.Sum256([]byte(text))
	source := intakeSource{ID: "50", ProjectID: "3", Version: 1, Content: &text, ContentHash: hex.EncodeToString(hash[:])}
	calls, posts := 0, 0
	revoked := false
	manifestAvailable := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tenants/1/agent-operations":
			ops := []map[string]string{}
			if manifestAvailable {
				ops = []map[string]string{{"name": "intake_suggestions.lookup", "method": "GET", "path": "/tenants/{tenant_id}/intake-suggestion-sources/{id}"}, {"name": "intake_suggestions.create", "method": "POST", "path": "/entities/{entity_id}/intake-suggestions"}}
			}
			respond(w, map[string]any{"operations": ops})
		case "/tenants/1/agents/7/inbox-events":
			respond(w, map[string]any{"items": []map[string]any{{"seq": 1, "kind": "STATE", "subject_id": "50"}}, "has_more": false})
		case "/tenants/1/intake-suggestion-sources/50":
			if revoked {
				w.WriteHeader(403)
				return
			}
			respond(w, source)
		case "/entities/3/intake-suggestions":
			posts++
			respond(w, map[string]any{"id": "81", "state": "SUGGESTED"})
		default:
			t.Error("unregistered contract called", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	model := func(context.Context, string, string, string) ([]byte, error) {
		calls++
		return []byte(`{"assessment":{"summary":"Observed"},"recommendation":"NEEDS_CLARIFICATION","reason":"Unspecified output"}`), nil
	}
	profile := workerProfile{PrincipalID: "7", Runtime: "CODEX", Model: "configured"}
	manifestAvailable = false
	if err := consumeAgentIntake(t.Context(), store, client, "1", profile, model); err == nil {
		t.Fatal("missing contract accepted")
	}
	manifestAvailable = true
	revoked = true
	if err := consumeAgentIntake(t.Context(), store, client, "1", profile, model); err != nil || calls != 0 || posts != 0 {
		t.Fatal("revoked source consumed", err, calls, posts)
	}
	revoked = false
	if err := consumeAgentIntake(t.Context(), store, client, "1", profile, model); err != nil || calls != 1 || posts != 1 {
		t.Fatal("actual source not consumed", err, calls, posts)
	}
}
