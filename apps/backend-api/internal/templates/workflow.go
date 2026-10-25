// Package templates owns versioned domain templates and their single workflow definition.
package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	ErrInvalid     = errors.New("templates: invalid definition")
	ErrConflict    = errors.New("templates: version or hash conflict")
	ErrUnavailable = errors.New("templates: capability unavailable")
	ErrForbidden   = errors.New("templates: forbidden")
	ErrNotFound    = errors.New("templates: not found")
)

type TemplateCriterion struct {
	Text               string `json:"text"`
	VerificationMethod string `json:"verification_method"`
}
type TemplateNode struct {
	Mandatory     bool                 `json:"mandatory,omitempty"`
	ID            string               `json:"id"`
	Kind          string               `json:"kind"`
	Name          string               `json:"name"`
	Objective     string               `json:"objective,omitempty"`
	Criteria      []TemplateCriterion  `json:"criteria,omitempty"`
	Gate          string               `json:"gate,omitempty"`
	Condition     json.RawMessage      `json:"condition,omitempty"`
	Items         []string             `json:"items,omitempty"`
	InputPath     string               `json:"input_path,omitempty"`
	MaxIterations int                  `json:"max_iterations,omitempty"`
	Budget        int                  `json:"budget,omitempty"`
	EscalateTo    string               `json:"escalate_to,omitempty"`
	Subflow       *TemplateWorkflowRef `json:"subflow,omitempty"`
	X             float64              `json:"x,omitempty"`
	Y             float64              `json:"y,omitempty"`
}
type TemplateWorkflowRef struct {
	WorkflowID string `json:"workflow_id"`
	Version    int    `json:"version"`
}
type TemplateEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Branch string `json:"branch,omitempty"`
}
type TemplateWorkflowDef struct {
	SchemaVersion  int               `json:"schema_version"`
	WorkflowID     string            `json:"workflow_id"`
	Version        int               `json:"version"`
	Name           string            `json:"name"`
	Parameters     map[string]any    `json:"parameters"`
	Nodes          []TemplateNode    `json:"nodes"`
	Edges          []TemplateEdge    `json:"edges"`
	EntryNodeID    string            `json:"entry_node_id"`
	OutputBindings map[string]string `json:"output_bindings"`
}
type TemplateDefinition struct {
	Domain           string              `json:"domain"`
	Semver           string              `json:"semver"`
	Workflow         TemplateWorkflowDef `json:"workflow"`
	Metadata         map[string]any      `json:"metadata"`
	ConstitutionSeed string              `json:"constitution_seed"`
}

// Hash canonicalizes maps through encoding/json; presentation positions are kept
// in the same export so reloading cannot drift from the saved definition.
func (d TemplateDefinition) Hash() (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Validate rejects implicit cycles, executable triggers and unbounded expansion.
// Child versions must be supplied by the caller from its authorized version reader.
func (d TemplateDefinition) Validate(children map[TemplateWorkflowRef]TemplateWorkflowDef) error {
	if _, err := progressPolicy(d); err != nil {
		return err
	}
	if _, err := rubricDefinition(d); err != nil {
		return err
	}
	if _, err := verifierDefinitions(d); err != nil {
		return err
	}
	if !slices.Contains([]string{"SOFTWARE", "VIDEO", "NOVEL", "TEAM_TASKS"}, d.Domain) || strings.TrimSpace(d.ConstitutionSeed) == "" {
		return ErrInvalid
	}
	var major, minor, patch int
	if n, err := fmt.Sscanf(d.Semver, "%d.%d.%d", &major, &minor, &patch); err != nil || n != 3 || major < 0 || minor < 0 || patch < 0 || fmt.Sprintf("%d.%d.%d", major, minor, patch) != d.Semver {
		return ErrInvalid
	}
	return validateWorkflow(d.Workflow, children, map[TemplateWorkflowRef]bool{})
}
func validateWorkflow(w TemplateWorkflowDef, children map[TemplateWorkflowRef]TemplateWorkflowDef, stack map[TemplateWorkflowRef]bool) error {
	ref := TemplateWorkflowRef{w.WorkflowID, w.Version}
	if stack[ref] || w.SchemaVersion != 1 || w.Version < 1 || strings.TrimSpace(w.WorkflowID) == "" || strings.TrimSpace(w.Name) == "" || len(w.Nodes) == 0 || len(w.Nodes) > 1000 || len(w.Edges) > 2000 {
		return ErrInvalid
	}
	stack[ref] = true
	defer delete(stack, ref)
	nodes := map[string]TemplateNode{}
	gates := map[string]bool{}
	for _, n := range w.Nodes {
		if n.Mandatory && n.Kind != "HUMAN_GATE" {
			return ErrInvalid
		}
		if n.ID == "" || strings.TrimSpace(n.Name) == "" {
			return ErrInvalid
		}
		if _, ok := nodes[n.ID]; ok {
			return ErrInvalid
		}
		nodes[n.ID] = n
		switch n.Kind {
		case "WORK_ITEM":
			if strings.TrimSpace(n.Objective) == "" || len(n.Criteria) == 0 || len(n.Criteria) > 50 {
				return ErrInvalid
			}
			for _, c := range n.Criteria {
				if strings.TrimSpace(c.Text) == "" || strings.TrimSpace(c.VerificationMethod) == "" {
					return ErrInvalid
				}
			}
		case "HUMAN_GATE":
			if n.Gate != "TEAM" && n.Gate != "CLIENT" {
				return ErrInvalid
			}
			gates[n.Gate] = true
		case "CONDITION":
			if err := validateLogic(n.Condition, 0); err != nil {
				return err
			}
		case "FAN_OUT":
			if (len(n.Items) == 0) == (n.InputPath == "") || len(n.Items) > 1000 {
				return ErrInvalid
			}
			seen := map[string]bool{}
			for _, id := range n.Items {
				if id == "" || seen[id] {
					return ErrInvalid
				}
				seen[id] = true
			}
		case "LOOP":
			if n.MaxIterations < 1 || n.MaxIterations > 100 || n.Budget < 1 || n.EscalateTo == "" || validateLogic(n.Condition, 0) != nil {
				return ErrInvalid
			}
		case "SUBFLOW":
			if n.Subflow == nil {
				return ErrInvalid
			}
			child, ok := children[*n.Subflow]
			if !ok || child.WorkflowID != n.Subflow.WorkflowID || child.Version != n.Subflow.Version {
				return ErrInvalid
			}
			if err := validateWorkflow(child, children, stack); err != nil {
				return err
			}
		case "TRANSFORM":
			if len(n.Condition) == 0 || validateLogic(n.Condition, 0) != nil {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	if _, ok := nodes[w.EntryNodeID]; !ok || !gates["TEAM"] || !gates["CLIENT"] {
		return ErrInvalid
	}
	indegree := map[string]int{}
	adj := map[string][]string{}
	edges := map[string]bool{}
	for _, e := range w.Edges {
		if e.ID == "" || edges[e.ID] {
			return ErrInvalid
		}
		edges[e.ID] = true
		if _, ok := nodes[e.Source]; !ok {
			return ErrInvalid
		}
		if _, ok := nodes[e.Target]; !ok {
			return ErrInvalid
		}
		if e.Source == e.Target {
			return ErrInvalid
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
		indegree[e.Target]++
	}
	if indegree[w.EntryNodeID] != 0 {
		return ErrInvalid
	}
	queue := []string{w.EntryNodeID}
	reached := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reached[id] {
			continue
		}
		reached[id] = true
		queue = append(queue, adj[id]...)
	}
	if len(reached) != len(nodes) {
		return ErrInvalid
	}
	queue = nil
	for id := range nodes {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	count := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		count++
		for _, to := range adj[id] {
			indegree[to]--
			if indegree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}
	if count != len(nodes) {
		return ErrInvalid
	}
	return nil
}

// Only a deliberately small JSON Logic vocabulary is executable. Unknown
// operators, arbitrary code, I/O and excessively deep expressions fail closed.
func validateLogic(raw json.RawMessage, depth int) error {
	if depth > 32 || len(raw) == 0 || len(raw) > 65536 {
		return ErrInvalid
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ErrInvalid
	}
	switch x := v.(type) {
	case map[string]any:
		if len(x) != 1 {
			return ErrInvalid
		}
		for op, arg := range x {
			if !slices.Contains([]string{"var", "==", "!=", ">", ">=", "<", "<=", "and", "or", "!", "in"}, op) {
				return ErrInvalid
			}
			b, _ := json.Marshal(arg)
			if err := validateLogic(b, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, arg := range x {
			b, _ := json.Marshal(arg)
			if err := validateLogic(b, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

type TemplatePatchOperation struct {
	Kind     string               `json:"kind"`
	ID       string               `json:"id,omitempty"`
	Node     *TemplateNode        `json:"node,omitempty"`
	Edge     *TemplateEdge        `json:"edge,omitempty"`
	Workflow *TemplateWorkflowDef `json:"workflow,omitempty"`
}
type TemplateWorkflowPatch struct {
	BaseVersion int                      `json:"base_version"`
	BaseHash    string                   `json:"base_hash"`
	Operations  []TemplatePatchOperation `json:"operations"`
	Reason      string                   `json:"reason"`
}

// ApplyPatch returns a detached candidate; even a failed validation never mutates
// the original version. Publishing remains a separate authorized command.
func ApplyPatch(d TemplateDefinition, p TemplateWorkflowPatch, children map[TemplateWorkflowRef]TemplateWorkflowDef) (TemplateDefinition, error) {
	h, err := d.Hash()
	if err != nil {
		return TemplateDefinition{}, err
	}
	if h != p.BaseHash || d.Workflow.Version != p.BaseVersion {
		return TemplateDefinition{}, ErrConflict
	}
	if strings.TrimSpace(p.Reason) == "" || len(p.Operations) == 0 {
		return TemplateDefinition{}, ErrInvalid
	}
	b, _ := json.Marshal(d)
	var out TemplateDefinition
	if err = json.Unmarshal(b, &out); err != nil {
		return TemplateDefinition{}, err
	}
	for _, op := range p.Operations {
		ni := slices.IndexFunc(out.Workflow.Nodes, func(n TemplateNode) bool { return n.ID == op.ID })
		ei := slices.IndexFunc(out.Workflow.Edges, func(e TemplateEdge) bool { return e.ID == op.ID })
		switch op.Kind {
		case "update_workflow":
			if op.Workflow == nil || op.Workflow.WorkflowID != out.Workflow.WorkflowID || op.Workflow.Version != out.Workflow.Version || op.Workflow.SchemaVersion != out.Workflow.SchemaVersion {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Name = op.Workflow.Name
			out.Workflow.Parameters = op.Workflow.Parameters
			out.Workflow.EntryNodeID = op.Workflow.EntryNodeID
			out.Workflow.OutputBindings = op.Workflow.OutputBindings
		case "add_node":
			if op.Node == nil || slices.ContainsFunc(out.Workflow.Nodes, func(n TemplateNode) bool { return n.ID == op.Node.ID }) {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Nodes = append(out.Workflow.Nodes, *op.Node)
		case "update_node":
			if ni < 0 || op.Node == nil || op.Node.ID != op.ID {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Nodes[ni] = *op.Node
		case "remove_node":
			if ni < 0 {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Nodes = slices.Delete(out.Workflow.Nodes, ni, ni+1)
		case "add_edge":
			if op.Edge == nil || slices.ContainsFunc(out.Workflow.Edges, func(e TemplateEdge) bool { return e.ID == op.Edge.ID }) {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Edges = append(out.Workflow.Edges, *op.Edge)
		case "update_edge":
			if ei < 0 || op.Edge == nil || op.Edge.ID != op.ID {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Edges[ei] = *op.Edge
		case "remove_edge":
			if ei < 0 {
				return TemplateDefinition{}, ErrInvalid
			}
			out.Workflow.Edges = slices.Delete(out.Workflow.Edges, ei, ei+1)
		default:
			return TemplateDefinition{}, ErrInvalid
		}
	}
	if err = out.Validate(children); err != nil {
		return TemplateDefinition{}, err
	}
	return out, nil
}
