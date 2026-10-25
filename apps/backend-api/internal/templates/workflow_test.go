package templates

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func testDefinition() TemplateDefinition {
	return TemplateDefinition{Domain: "SOFTWARE", Semver: "1.0.0", ConstitutionSeed: "# Working agreement", Metadata: map[string]any{"method": "agile"}, Workflow: TemplateWorkflowDef{SchemaVersion: 1, WorkflowID: "software", Version: 1, Name: "Software", Parameters: map[string]any{}, EntryNodeID: "work", OutputBindings: map[string]string{}, Nodes: []TemplateNode{{ID: "work", Kind: "WORK_ITEM", Name: "Build", Objective: "Approved brief", Criteria: []TemplateCriterion{{Text: "Tests pass", VerificationMethod: "Run tests"}}}, {ID: "team", Kind: "HUMAN_GATE", Name: "Review", Gate: "TEAM"}, {ID: "client", Kind: "HUMAN_GATE", Name: "Accept", Gate: "CLIENT"}}, Edges: []TemplateEdge{{ID: "a", Source: "work", Target: "team"}, {ID: "b", Source: "team", Target: "client"}}}}
}
func cloneDefinition(d TemplateDefinition) TemplateDefinition {
	b, _ := json.Marshal(d)
	var out TemplateDefinition
	_ = json.Unmarshal(b, &out)
	return out
}
func TestWorkflowValidationRejectsUnsafeGraphs(t *testing.T) {
	base := testDefinition()
	require.NoError(t, base.Validate(nil))
	cases := map[string]func(*TemplateDefinition){
		"domain": func(d *TemplateDefinition) { d.Domain = "UNKNOWN" }, "seed": func(d *TemplateDefinition) { d.ConstitutionSeed = " " }, "semver": func(d *TemplateDefinition) { d.Semver = "1.0.0tail" }, "negative_semver": func(d *TemplateDefinition) { d.Semver = "-1.0.0" },
		"schema": func(d *TemplateDefinition) { d.Workflow.SchemaVersion = 2 }, "version": func(d *TemplateDefinition) { d.Workflow.Version = 0 }, "workflow_id": func(d *TemplateDefinition) { d.Workflow.WorkflowID = "" }, "name": func(d *TemplateDefinition) { d.Workflow.Name = " " }, "empty": func(d *TemplateDefinition) { d.Workflow.Nodes = nil }, "too_large": func(d *TemplateDefinition) { d.Workflow.Nodes = make([]TemplateNode, 1001) }, "too_many_edges": func(d *TemplateDefinition) { d.Workflow.Edges = make([]TemplateEdge, 2001) },
		"node_id": func(d *TemplateDefinition) { d.Workflow.Nodes[0].ID = "" }, "node_name": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Name = "" }, "duplicate_node": func(d *TemplateDefinition) { d.Workflow.Nodes[1].ID = "work" }, "objective": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Objective = "" }, "no_criteria": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Criteria = nil }, "criterion_text": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Criteria[0].Text = "" }, "criterion_method": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Criteria[0].VerificationMethod = "" },
		"gate": func(d *TemplateDefinition) { d.Workflow.Nodes[1].Gate = "AGENT" }, "missing_team": func(d *TemplateDefinition) { d.Workflow.Nodes[1].Gate = "CLIENT" }, "missing_client": func(d *TemplateDefinition) { d.Workflow.Nodes[2].Gate = "TEAM" }, "entry": func(d *TemplateDefinition) { d.Workflow.EntryNodeID = "unknown" }, "trigger": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Kind = "TRIGGER" },
		"edge_id": func(d *TemplateDefinition) { d.Workflow.Edges[0].ID = "" }, "duplicate_edge": func(d *TemplateDefinition) { d.Workflow.Edges[1].ID = "a" }, "source": func(d *TemplateDefinition) { d.Workflow.Edges[0].Source = "missing" }, "target": func(d *TemplateDefinition) { d.Workflow.Edges[0].Target = "missing" }, "self": func(d *TemplateDefinition) { d.Workflow.Edges[0].Target = "work" }, "entry_incoming": func(d *TemplateDefinition) {
			d.Workflow.Edges = append(d.Workflow.Edges, TemplateEdge{ID: "c", Source: "client", Target: "work"})
		}, "cycle": func(d *TemplateDefinition) {
			d.Workflow.Edges = append(d.Workflow.Edges, TemplateEdge{ID: "c", Source: "client", Target: "team"})
		}, "unreachable": func(d *TemplateDefinition) { d.Workflow.Edges = d.Workflow.Edges[:1] },
		"fan_empty": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Kind = "FAN_OUT" }, "fan_both": func(d *TemplateDefinition) {
			d.Workflow.Nodes[0].Kind = "FAN_OUT"
			d.Workflow.Nodes[0].Items = []string{"a"}
			d.Workflow.Nodes[0].InputPath = "brief"
		}, "fan_duplicate": func(d *TemplateDefinition) {
			d.Workflow.Nodes[0].Kind = "FAN_OUT"
			d.Workflow.Nodes[0].Items = []string{"a", "a"}
		}, "fan_empty_id": func(d *TemplateDefinition) {
			d.Workflow.Nodes[0].Kind = "FAN_OUT"
			d.Workflow.Nodes[0].Items = []string{""}
		},
		"loop": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Kind = "LOOP" }, "subflow": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Kind = "SUBFLOW" }, "missing_subflow": func(d *TemplateDefinition) {
			d.Workflow.Nodes[0].Kind = "SUBFLOW"
			d.Workflow.Nodes[0].Subflow = &TemplateWorkflowRef{"missing", 1}
		}, "transform": func(d *TemplateDefinition) { d.Workflow.Nodes[0].Kind = "TRANSFORM" }, "condition": func(d *TemplateDefinition) {
			d.Workflow.Nodes[0].Kind = "CONDITION"
			d.Workflow.Nodes[0].Condition = json.RawMessage(`{"exec":"rm"}`)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := cloneDefinition(base)
			mutate(&d)
			require.ErrorIs(t, d.Validate(nil), ErrInvalid)
		})
	}
	for _, node := range []TemplateNode{{Kind: "CONDITION", Condition: json.RawMessage(`{"==":[{"var":"method"},"agile"]}`)}, {Kind: "TRANSFORM", Condition: json.RawMessage(`{"var":"brief"}`)}, {Kind: "FAN_OUT", Items: []string{"a", "b"}}, {Kind: "FAN_OUT", InputPath: "signed_brief.items"}, {Kind: "LOOP", MaxIterations: 5, Budget: 10, EscalateTo: "accountable", Condition: json.RawMessage(`{"!":[false]}`)}} {
		d := cloneDefinition(base)
		node.ID, node.Name = "work", "Control"
		d.Workflow.Nodes[0] = node
		require.NoError(t, d.Validate(nil))
	}
	child := testDefinition().Workflow
	child.WorkflowID = "child"
	d := cloneDefinition(base)
	d.Workflow.Nodes[0] = TemplateNode{ID: "work", Name: "Child", Kind: "SUBFLOW", Subflow: &TemplateWorkflowRef{"child", 1}}
	require.NoError(t, d.Validate(map[TemplateWorkflowRef]TemplateWorkflowDef{{"child", 1}: child}))
	child.Nodes[0] = TemplateNode{ID: "work", Name: "Recursive", Kind: "SUBFLOW", Subflow: &TemplateWorkflowRef{"software", 1}}
	require.ErrorIs(t, d.Validate(map[TemplateWorkflowRef]TemplateWorkflowDef{{"child", 1}: child, {"software", 1}: d.Workflow}), ErrInvalid)
}
func TestLogicRejectsCodeAndDepth(t *testing.T) {
	for _, raw := range []string{"", `{`, strings.Repeat(" ", 65537), `{"==":[],"var":"x"}`, `{"http.get":"https://example.com"}`, strings.Repeat(`{"!":`, 34) + `true` + strings.Repeat(`}`, 34)} {
		require.ErrorIs(t, validateLogic(json.RawMessage(raw), 0), ErrInvalid)
	}
	require.NoError(t, validateLogic(json.RawMessage(`{"and":[{"in":["a",["a","b"]]},{">=":[2,1]}]}`), 0))
}
func TestPatchesAreDetachedAndHashBound(t *testing.T) {
	d := testDefinition()
	h, e := d.Hash()
	require.NoError(t, e)
	p := TemplateWorkflowPatch{BaseVersion: 1, BaseHash: h, Reason: "Rename", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: "work", Node: &TemplateNode{ID: "work", Name: "New", Kind: "WORK_ITEM", Objective: "Approved brief", Criteria: d.Workflow.Nodes[0].Criteria}}}}
	out, e := ApplyPatch(d, p, nil)
	require.NoError(t, e)
	require.Equal(t, "Build", d.Workflow.Nodes[0].Name)
	require.Equal(t, "New", out.Workflow.Nodes[0].Name)
	h2, e := out.Hash()
	require.NoError(t, e)
	require.NotEqual(t, h, h2)
	p.BaseHash = h2
	_, e = ApplyPatch(d, p, nil)
	require.ErrorIs(t, e, ErrConflict)
	p.BaseHash = h
	p.BaseVersion = 2
	_, e = ApplyPatch(d, p, nil)
	require.ErrorIs(t, e, ErrConflict)
	p.BaseVersion = 1
	p.Reason = ""
	_, e = ApplyPatch(d, p, nil)
	require.ErrorIs(t, e, ErrInvalid)
	p.Reason = "Change"
	for _, op := range []TemplatePatchOperation{{Kind: "unknown"}, {Kind: "add_node"}, {Kind: "add_edge"}, {Kind: "update_node", ID: "missing"}, {Kind: "update_edge", ID: "missing"}, {Kind: "remove_node", ID: "missing"}, {Kind: "remove_edge", ID: "missing"}, {Kind: "add_node", Node: &d.Workflow.Nodes[0]}, {Kind: "add_edge", Edge: &d.Workflow.Edges[0]}, {Kind: "update_workflow"}, {Kind: "update_workflow", Workflow: &TemplateWorkflowDef{WorkflowID: "wrong"}}, {Kind: "remove_node", ID: "team"}} {
		p.Operations = []TemplatePatchOperation{op}
		_, e = ApplyPatch(d, p, nil)
		require.Error(t, e)
	}
	extra := TemplateNode{ID: "extra", Name: "Extra", Kind: "HUMAN_GATE", Gate: "TEAM"}
	edge := TemplateEdge{ID: "extra-edge", Source: "client", Target: "extra"}
	w := d.Workflow
	w.Name = "Renamed"
	p.Operations = []TemplatePatchOperation{{Kind: "add_node", Node: &extra}, {Kind: "add_edge", Edge: &edge}, {Kind: "update_edge", ID: "b", Edge: &d.Workflow.Edges[1]}, {Kind: "update_workflow", Workflow: &w}}
	out, e = ApplyPatch(d, p, nil)
	require.NoError(t, e)
	require.Len(t, out.Workflow.Nodes, 4)
	h, _ = out.Hash()
	p.BaseHash = h
	p.Operations = []TemplatePatchOperation{{Kind: "remove_edge", ID: "extra-edge"}, {Kind: "remove_node", ID: "extra"}}
	out, e = ApplyPatch(out, p, nil)
	require.NoError(t, e)
	require.Len(t, out.Workflow.Nodes, 3)
	d.Metadata["invalid"] = make(chan int)
	_, e = d.Hash()
	require.Error(t, e)
	_, e = ApplyPatch(d, p, nil)
	require.Error(t, e)
}
