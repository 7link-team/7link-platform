package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"7link.com/runner/internal/executor"
)

func TestVerificationProtocolPostsObservedSnapshotFailureWithoutSuccess(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/runner/verifications/5/receipts" {
			t.Error("wrong path", r.URL.Path)
		}
		var body struct {
			Nonce       string                           `json:"nonce"`
			Observation executor.VerificationObservation `json:"observation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Nonce != "nonce" || body.Observation.ExitCode != nil || body.Observation.Reason != "SNAPSHOT_UNAVAILABLE" {
			t.Errorf("fabricated process result: %+v", body)
		}
		seen = true
		respond(w, true)
	}))
	defer server.Close()
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.store = testStore(t)
	p := executor.VerificationPlan{ID: "5", Nonce: "nonce", ResultID: "6", ResultVersion: 1, CriterionID: "7", Method: "COMMAND", SourceWorkdir: t.TempDir(), HeadCommit: strings.Repeat("a", 40), Argv: []string{"git", "--version"}, TimeoutSec: 5}
	if err = runVerification(t.Context(), c, fence{MachineID: "11", SessionID: "12", Epoch: 1}, p); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no observation posted")
	}
}

func TestVerificationVisualUploadsActualHashedPNGThroughReceipt(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		hideServiceWindow(cmd)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git fixture: %v", e)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", root)
	git("-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "visual upload fixture")
	head := git("-C", root, "rev-parse", "HEAD")
	preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><p>Actual preview</p>"))
	}))
	defer preview.Close()
	var received executor.VerificationObservation
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Nonce       string                           `json:"nonce"`
			Observation executor.VerificationObservation `json:"observation"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body.Nonce != "nonce" || r.URL.Path != "/runner/verifications/5/receipts" {
			t.Error("receipt binding lost")
		}
		received = body.Observation
		respond(w, true)
	}))
	defer api.Close()
	c, e := NewClient(api.URL)
	if e != nil {
		t.Fatal(e)
	}
	c.store = testStore(t)
	p := executor.VerificationPlan{ID: "5", Nonce: "nonce", ResultID: "6", ResultVersion: 1, CriterionID: "7", Method: "VISUAL", SourceWorkdir: root, HeadCommit: head, Argv: []string{preview.URL, "320", "240"}, TimeoutSec: 35}
	if e = runVerification(t.Context(), c, fence{MachineID: "11", SessionID: "12", Epoch: 1}, p); e != nil {
		t.Fatal(e)
	}
	if received.Reason == "BROWSER_UNAVAILABLE" {
		t.Skip("BROWSER_UNAVAILABLE: explicit receipt received")
	}
	if received.Reason != "VISUAL_REVIEW_REQUIRED" || len(received.Screenshots) != 1 || received.ExitCode != nil {
		t.Fatalf("capture receipt reason=%s screenshots=%d", received.Reason, len(received.Screenshots))
	}
	s := received.Screenshots[0]
	h := sha256.Sum256(s.Data)
	if s.SHA256 != hex.EncodeToString(h[:]) || s.Width != 320 || s.Height != 240 || s.Route != preview.URL || received.HeadCommit != head {
		t.Fatal("actual screenshot bytes or binding lost")
	}
	t.Logf("Uploaded browser=%s PNG bytes=%d SHA256=%s", received.BrowserProvider, len(s.Data), s.SHA256)
}
