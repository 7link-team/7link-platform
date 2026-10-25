package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestMergeProtocolPostsActualGitOutcomeAndExactFence(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("git unavailable")
	}
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "unavailable", true: "merged"}[available], func(t *testing.T) {
			root := t.TempDir()
			source := t.TempDir()
			git := func(dir string, args ...string) string {
				t.Helper()
				c := exec.Command("git", append([]string{"-C", dir}, args...)...)
				hideServiceWindow(c)
				b, e := c.CombinedOutput()
				if e != nil {
					t.Fatalf("git %v: %v %s", args, e, b)
				}
				return strings.TrimSpace(string(b))
			}
			p := executor.MergePlan{ID: "5", ExecutionID: "6", Nonce: strings.Repeat("a", 64), Workspace: root, SourceWorkdir: source, SourceCommit: strings.Repeat("b", 40), TargetBranch: "main", DeadlineAt: time.Now().Add(time.Minute)}
			if available {
				git(root, "init", "-b", "main")
				git(root, "config", "user.name", "Test")
				git(root, "config", "user.email", "test@example.invalid")
				git(root, "commit", "--allow-empty", "-m", "base")
				// worktree add needs an unused path, rather than the existing temp root.
				p.SourceWorkdir = filepath.Join(source, "worktree")
				git(root, "worktree", "add", "--detach", p.SourceWorkdir, "HEAD")
				if e := os.WriteFile(filepath.Join(p.SourceWorkdir, "result.txt"), []byte("actual merge"), 0600); e != nil {
					t.Fatal(e)
				}
				git(p.SourceWorkdir, "add", "result.txt")
				git(p.SourceWorkdir, "commit", "-m", "source")
				p.SourceCommit = git(p.SourceWorkdir, "rev-parse", "HEAD")
			}
			wire, _ := json.Marshal(p)
			digest := sha256.Sum256(wire)
			seen := 0
			f := fence{MachineID: "11", SessionID: "12", Epoch: 3}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen++
				if r.Method != "POST" || r.URL.Path != "/runner/merges/5/receipts" {
					t.Error("wrong merge endpoint", r.Method, r.URL.Path)
				}
				var body struct {
					Fence       fence                     `json:"fence"`
					Nonce       string                    `json:"nonce"`
					Observation executor.MergeObservation `json:"observation"`
				}
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Error(e)
				}
				if body.Fence != f || body.Nonce != p.Nonce || body.Observation.PlanHash != hex.EncodeToString(digest[:]) || body.Observation.SourceCommit != p.SourceCommit {
					t.Errorf("command/fence binding lost: %+v", body)
				}
				if available {
					if body.Observation.State != "MERGED" || body.Observation.MergeCommit != git(root, "rev-parse", "HEAD") || body.Observation.Pushed {
						t.Errorf("actual merge not reported: %+v", body.Observation)
					}
				} else if body.Observation.State != "FAILED" || body.Observation.Reason != "WORKSPACE_UNAVAILABLE" || body.Observation.MergeCommit != "" || body.Observation.Pushed {
					t.Errorf("failure fabricated success: %+v", body.Observation)
				}
				respond(w, true)
			}))
			defer server.Close()
			c, e := NewClient(server.URL)
			if e != nil {
				t.Fatal(e)
			}
			c.store = testStore(t)
			if e = runMerge(t.Context(), c, f, p); e != nil {
				t.Fatal(e)
			}
			if seen != 1 {
				t.Fatal("expected exactly one immutable outcome receipt", seen)
			}
		})
	}
}

func TestMergeUnsupportedPlanDoesNotPostReceipt(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = true; respond(w, true) }))
	defer server.Close()
	c, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	c.store = testStore(t)
	if e = runMerge(t.Context(), c, fence{}, executor.MergePlan{}); e != executor.ErrUnsupported {
		t.Fatal("invalid plan accepted", e)
	}
	if seen {
		t.Fatal("invalid plan manufactured a merge receipt")
	}
}
