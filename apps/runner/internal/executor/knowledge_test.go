package executor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDistillationCapturesOnlyModelAuthoredSkillDocumentForExplicitPurpose(t *testing.T) {
	document := "---\nname: verified-guidance\ndescription: Reuse independent evidence\ntags: [review]\n---\nKeep concrete verification receipts."
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		plan := Plan{Runtime: runtime, Distillation: &DistillationPlan{ID: "1", AgentPrincipalID: "2", SourceExecutionID: "3"}}
		observation := Observation{Kind: "LOG", Content: document}
		if runtime == "CLAUDE_CODE" {
			body, err := json.Marshal(map[string]any{"type": "result", "subtype": "success", "result": document})
			if err != nil {
				t.Fatal(err)
			}
			observation = Observation{Kind: "RUNTIME_COMPLETED", Content: string(body)}
		}
		if got := DistilledOutput(plan, observation); got != document {
			t.Fatalf("%s lost actual model document: %q", runtime, got)
		}
		if got := DistilledOutput(plan, Observation{Kind: "RUNTIME_EVENT", Content: document}); got != "" {
			t.Fatalf("%s accepted tool echo", runtime)
		}
		plan.Distillation = nil
		if got := DistilledOutput(plan, observation); got != "" {
			t.Fatalf("%s captured ordinary execution as distillation", runtime)
		}
	}
	if got := DistilledOutput(Plan{Runtime: "CODEX", Distillation: &DistillationPlan{}}, Observation{Kind: "LOG", Content: "Runtime claimed success without a document"}); got != "" {
		t.Fatal("non-document became a draft candidate")
	}
}

func TestFrozenKnowledgeReachesRuntimeAndOnlyModelOutputCreatesCitations(t *testing.T) {
	plan := runtimeFixture(t)
	plan.Model = "quality-feedback"
	item := KnowledgeItem{EntryID: "9007199254740995", Version: 2, Label: "[K1]", Title: "Evidence policy", Body: "Keep source receipts."}
	item.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(item.Title+"\n"+item.Body)))
	rule := KnowledgeItem{EntryID: "9007199254740996", Version: 1, Label: "[R1]", Title: "Approved rule", Body: "Preserve independent approval."}
	rule.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(rule.Title+"\n"+rule.Body)))
	// A budgeted snapshot retains the canonical hash for provenance while the
	// runtime verifies the bytes it actually receives using SnapshotHash.
	item.SnapshotHash = item.ContentHash
	item.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(item.Title+"\n"+item.Body+" Full canonical text omitted by budget.")))
	plan.Knowledge = []KnowledgeItem{rule, item}
	for _, kind := range []string{"CODEX", "CLAUDE_CODE"} {
		plan.Runtime = kind
		var stdin string
		_, err := Run(t.Context(), plan, func(o Observation) error {
			if kind == "CODEX" && o.Kind == "LOG" {
				stdin = o.Content
			}
			if kind == "CLAUDE_CODE" && o.Kind == "RUNTIME_COMPLETED" {
				var event struct {
					Result string `json:"result"`
				}
				if err := json.Unmarshal([]byte(o.Content), &event); err != nil {
					return err
				}
				stdin = event.Result
			}
			return nil
		})
		if err != nil || !strings.Contains(stdin, item.Label+" "+item.Title) || !strings.Contains(stdin, item.Body) || !strings.Contains(stdin, "kref:entry/"+item.EntryID+"#"+item.ContentHash) {
			t.Fatalf("%s knowledge did not reach actual stdin: %v", kind, err)
		}
		if !strings.Contains(stdin, rule.Label+" "+rule.Title) || !strings.Contains(stdin, rule.Body) || strings.Contains(stdin, "Full canonical text omitted by budget") {
			t.Fatalf("%s approved rule or budgeted snapshot did not reach actual stdin", kind)
		}
	}
	plan.Runtime = "CODEX"
	if got := CitedOutput(plan, Observation{Kind: "RUNTIME_EVENT", Content: "Tool echoed [K1]"}); got != "" {
		t.Fatal("tool echo counted as citation")
	}
	if got := CitedOutput(plan, Observation{Kind: "LOG", Content: "Unknown [K2]"}); got != "" {
		t.Fatal("unknown citation counted")
	}
	if got := CitedOutput(plan, Observation{Kind: "LOG", Content: "Answer cites [K1]"}); got != "Answer cites [K1]" {
		t.Fatal("model citation lost")
	}
	plan.Runtime = "CLAUDE_CODE"
	if got := CitedOutput(plan, Observation{Kind: "RUNTIME_COMPLETED", Content: `{"type":"result","subtype":"success","result":"Answer cites [K1]"}`}); got != "Answer cites [K1]" {
		t.Fatal("Claude citation lost")
	}
	if got := CitedOutput(plan, Observation{Kind: "RUNTIME_COMPLETED", Content: `{"type":"result","subtype":"success","result":"Answer cites [R1]"}`}); got != "Answer cites [R1]" {
		t.Fatal("approved rule citation lost")
	}
	plan.Knowledge[1].Body = "tampered"
	if _, _, err := Command(plan); err != ErrUnsupported {
		t.Fatal("tampered frozen knowledge accepted", err)
	}
}

func TestStructuredFeedbackLabelsReachRuntimeAndOnlyModelOutputCounts(t *testing.T) {
	plan := runtimeFixture(t)
	plan.Model = "quality-feedback"
	plan.QualityFeedback = `{"feedback":[{"category":"FUNCTIONAL_DEFECT","expected":"PASSED"}]}`
	if !strings.Contains(feedbackPrompt(plan.QualityFeedback), "[F1]") {
		t.Fatal("missing feedback reference")
	}
	plan.Runtime = "CODEX"
	if got := CitedOutput(plan, Observation{Kind: "LOG", Content: "Resolved [F1]"}); got == "" {
		t.Fatal("real feedback citation lost")
	}
	if got := CitedOutput(plan, Observation{Kind: "RUNTIME_EVENT", Content: "Tool echoes [F1]"}); got != "" {
		t.Fatal("tool echo counted")
	}
	if got := CitedOutput(plan, Observation{Kind: "LOG", Content: "Unknown [F2]"}); got != "" {
		t.Fatal("invented feedback citation counted")
	}
	var stdin string
	_, err := Run(t.Context(), plan, func(o Observation) error {
		if o.Kind == "LOG" {
			stdin = o.Content
		}
		return nil
	})
	if err != nil || !strings.Contains(stdin, "[F1]") {
		t.Fatalf("feedback did not reach actual runtime stdin: %v", err)
	}
}
