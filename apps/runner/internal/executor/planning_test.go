package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func planningFixture(t *testing.T) Plan {
	t.Helper()
	p := runtimeFixture(t)
	branch, e := workspaceGit(t.Context(), p.Workdir, "symbolic-ref", "--short", "HEAD")
	if e != nil {
		t.Fatal(e)
	}
	p.Planning = &PlanningPlan{Binding: json.RawMessage(`{"contract_hash":"frozen"}`), Criteria: json.RawMessage(`[{"id":"41"}]`), DefaultBranch: strings.TrimSpace(string(branch))}
	p.Model = "planning-output"
	p.Objective = `{"files":["feature.go"],"criteria_coverage":[{"criterion_id":"41","approach":"implement","verification":"test"}],"risks":[],"out_of_scope_confirmed":true,"binding":{"contract_hash":"frozen"}}`
	return p
}

func TestPlanningCapturesActualReadonlyRuntimeDocumentWithoutClaim(t *testing.T) {
	p := planningFixture(t)
	for _, kind := range []string{"CODEX", "CLAUDE_CODE"} {
		p.Runtime = kind
		result, e := Run(t.Context(), p, nil)
		if e != nil || !result.RuntimeCompleted || result.PlanningDocument != p.Objective || result.HeadCommit != p.BaseCommit || result.Workspace != "" || len(result.Diff) != 0 || len(result.Artifacts) != 0 {
			t.Fatalf("%s result=%+v error=%v", kind, result, e)
		}
		status, e := workspaceGit(t.Context(), p.Workdir, "status", "--porcelain", "--untracked-files=all")
		if e != nil || len(status) != 0 {
			t.Fatalf("planning changed repository: %s %v", status, e)
		}
	}
}

func TestPlanningRejectsWritePurposeDirtyTreeWrongHeadAndMalformedOutput(t *testing.T) {
	for _, kind := range []string{"write", "dirty", "head", "malformed", "mutation"} {
		t.Run(kind, func(t *testing.T) {
			p := planningFixture(t)
			switch kind {
			case "write":
				p.AuthorizationMode = "WORKSPACE_WRITE"
				if _, _, e := Command(p); e == nil {
					t.Fatal("write planning command accepted")
				}
			case "dirty":
				if e := os.WriteFile(filepath.Join(p.Workdir, "untracked"), []byte("dirty"), 0600); e != nil {
					t.Fatal(e)
				}
			case "head":
				p.BaseCommit = strings.Repeat("a", 40)
			case "malformed":
				p.Objective = "actual non JSON output"
			}
			_, e := Run(t.Context(), p, func(o Observation) error {
				if kind == "mutation" && o.Kind == "RUNTIME_COMPLETED" {
					return os.WriteFile(filepath.Join(p.Workdir, "runtime-write"), []byte("changed"), 0600)
				}
				return nil
			})
			if e == nil {
				t.Fatal("invalid planning accepted", kind)
			}
		})
	}
}

func TestPlanningLargeFrozenInputsReachActualRuntimeStdinIntact(t *testing.T) {
	criteria := make([]map[string]string, 12)
	for i := range criteria {
		criteria[i] = map[string]string{"id": "41", "text": strings.Repeat("c", 2000), "method": "test"}
	}
	criteriaJSON, e := json.Marshal(criteria)
	if e != nil {
		t.Fatal(e)
	}
	risks := make([]string, 12)
	for i := range risks {
		risks[i] = strings.Repeat("r", 2000)
	}
	content, e := json.Marshal(map[string]any{"files": []string{"feature.go"}, "risks": risks, "out_of_scope_confirmed": true})
	if e != nil || len(content) <= 20000 || len(criteriaJSON) <= 20000 {
		t.Fatalf("large frozen fixtures: %v", e)
	}
	for _, phase := range []string{"planning", "review", "approved"} {
		for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
			t.Run(phase+"/"+runtime, func(t *testing.T) {
				p := planningFixture(t)
				p.Model, p.Runtime, p.Objective = "planning-observe", runtime, strings.Repeat("o", 20000)
				var frozen []byte
				switch phase {
				case "planning":
					p.Planning.Criteria, p.Planning.Feedback = criteriaJSON, "Retain this exact rejection finding."
					frozen, e = json.Marshal(p.Planning)
				case "review":
					p.Planning.Review = append(append([]byte(`{"plan_id":"81","content":`), content...), '}')
					frozen, e = json.Marshal(p.Planning)
				case "approved":
					p.Planning, p.ApprovedPlan = nil, content
					frozen = content
				}
				if e != nil {
					t.Fatal(e)
				}
				var document string
				result, err := Run(t.Context(), p, func(o Observation) error {
					if text := modelOutput(p, o); text != "" {
						document = text
					}
					return nil
				})
				var observed struct {
					Prompt string `json:"observed_prompt"`
				}
				if err != nil || !result.RuntimeCompleted || json.Unmarshal([]byte(document), &observed) != nil {
					t.Fatalf("actual prompt observation: completed=%v error=%v", result.RuntimeCompleted, err)
				}
				if !strings.HasPrefix(observed.Prompt, p.Objective) || !strings.Contains(observed.Prompt, string(frozen)) || len(observed.Prompt) <= 40000 {
					t.Fatalf("frozen inputs truncated or original objective lost: bytes=%d", len(observed.Prompt))
				}
				if phase == "review" && !strings.Contains(observed.Prompt, "Independently review") || phase == "approved" && !strings.Contains(observed.Prompt, "Implement the approved frozen execution plan exactly") {
					t.Fatal("runtime purpose instructions missing")
				}
			})
		}
	}
}
