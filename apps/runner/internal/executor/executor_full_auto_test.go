package executor

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFullAutoCommandRequiresGrantAndUsesRuntimePermissionMode(t *testing.T) {
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		t.Run(runtime, func(t *testing.T) {
			p := Plan{Runtime: runtime, Model: "configured-model", Workdir: filepath.Join(t.TempDir(), "repo"), Objective: "Perform the explicitly approved work", AuthorizationMode: "FULL_AUTO"}
			for _, grant := range []string{"", "0", "-1", "not-an-id", "123;touch evil"} {
				p.FullAutoGrantID = grant
				_, _, e := Command(p)
				if !errors.Is(e, ErrUnsupported) {
					t.Fatalf("grant %q: error = %v", grant, e)
				}
			}
			p.FullAutoGrantID = "123"
			_, args, e := Command(p)
			if e != nil {
				t.Fatal(e)
			}
			if runtime == "CODEX" {
				if !slices.Contains(args, "danger-full-access") {
					t.Fatal(args)
				}
			} else {
				if !slices.Contains(args, "bypassPermissions") {
					t.Fatal(args)
				}
				if slices.Contains(args, "--permission-prompt-tool") {
					t.Fatal(args)
				}
			}
		})
	}
}
func TestFullAutoMarkerTravelsInTrustedWorkerEnvelopeOnly(t *testing.T) {
	p := Plan{AuthorizationMode: "FULL_AUTO", FullAutoGrantID: "123"}
	b, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "full_auto_grant_id") {
		t.Fatal(string(b))
	}
	var posted Plan
	if e = json.Unmarshal([]byte(`{"authorization_mode":"FULL_AUTO","full_auto_grant_id":"999"}`), &posted); e != nil {
		t.Fatal(e)
	}
	if posted.FullAutoGrantID != "" {
		t.Fatal(posted.FullAutoGrantID)
	}
	b, e = json.Marshal(workerRequest{Plan: p, FullAutoGrantID: p.FullAutoGrantID})
	if e != nil {
		t.Fatal(e)
	}
	var received workerRequest
	if e = json.Unmarshal(b, &received); e != nil {
		t.Fatal(e)
	}
	if received.FullAutoGrantID != "123" {
		t.Fatal(received.FullAutoGrantID)
	}
	if received.Plan.FullAutoGrantID != "" {
		t.Fatal(received.Plan.FullAutoGrantID)
	}
}
