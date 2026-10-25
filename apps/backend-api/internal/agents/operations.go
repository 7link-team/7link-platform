package agents

import (
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"

	"regexp"
	"sort"
	"strings"
	"sync"
)

// OperationRegistry is the provider-owned startup capability for modules to
// publish callable operations. A manifest describes contracts, never grants them.
type OperationRegistry struct {
	mu         sync.RWMutex
	operations map[string]Operation
	sealed     bool
}
type OperationParameter struct {
	Name     string          `json:"name"`
	In       string          `json:"in"`
	Required bool            `json:"required"`
	Schema   json.RawMessage `json:"schema"`
}
type Operation struct {
	HumanConfirmation   string               `json:"human_confirmation"`
	Quota               string               `json:"quota"`
	Phase               string               `json:"phase"`
	Parameters          []OperationParameter `json:"parameters"`
	Name                string               `json:"name"`
	Summary             string               `json:"summary"`
	Method              string               `json:"method"`
	Path                string               `json:"path"`
	InputSchema         json.RawMessage      `json:"input_schema"`
	OutputSchema        json.RawMessage      `json:"output_schema"`
	SideEffects         []string             `json:"side_effects"`
	IdempotencyRequired bool                 `json:"idempotency_required"`
	KeyRule             string               `json:"key_rule"`
	RequiredPermission  string               `json:"required_permission"`
	Via                 string               `json:"via"`
	Delegable           bool                 `json:"delegable"`
	HumanOnly           bool                 `json:"human_only"`
}
type Next struct {
	Operation string `json:"operation"`
	Reason    string `json:"reason,omitempty"`
}

func NewOperationRegistry() *OperationRegistry {
	return &OperationRegistry{operations: map[string]Operation{}}
}

var operationName = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)

func (r *OperationRegistry) Register(op Operation) error {
	if !operationName.MatchString(op.Name) || op.Summary == "" || op.Path == "" || op.Method == "" || !json.Valid(op.InputSchema) || !json.Valid(op.OutputSchema) || op.SideEffects == nil || (op.Via != "SELF" && op.RequiredPermission == "") || (op.Via == "SELF" && op.RequiredPermission != "") || (op.HumanOnly && op.Delegable) || (op.IdempotencyRequired && op.KeyRule == "") {
		return ErrInvalid
	}
	// Require actual schema objects, not JSON primitives.
	for _, raw := range []json.RawMessage{op.InputSchema, op.OutputSchema} {
		var schema map[string]any
		if json.Unmarshal(raw, &schema) != nil || schema == nil || schema["type"] != "object" {
			return ErrInvalid
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return ErrConflict
	}
	if _, exists := r.operations[op.Name]; exists {
		return errors.New("agents: duplicate operation")
	}
	if op.HumanConfirmation == "" {
		op.HumanConfirmation = "NONE"
	}
	if op.Quota == "" {
		op.Quota = "NONE"
	}
	if op.Phase == "" {
		op.Phase = "MVP"
	}
	if op.Name == "agent_reminders.create" || op.Name == "channel.post" {
		op.Quota = "RATE_LIMIT"
	}
	op.Parameters = cloneParameters(op.Parameters)
	op.InputSchema = append(json.RawMessage(nil), op.InputSchema...)
	op.OutputSchema = append(json.RawMessage(nil), op.OutputSchema...)
	op.SideEffects = append([]string{}, op.SideEffects...)
	r.operations[op.Name] = op
	return nil
}
func (r *OperationRegistry) List() []Operation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Operation, 0, len(r.operations))
	for _, op := range r.operations {
		op.Parameters = cloneParameters(op.Parameters)
		op.InputSchema = append(json.RawMessage(nil), op.InputSchema...)
		op.OutputSchema = append(json.RawMessage(nil), op.OutputSchema...)
		op.SideEffects = append([]string{}, op.SideEffects...)
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Manifest schemas are derived from final registered routes, including the
// tenant mount. Inline references make the document independently consumable.
func inlineSchema(document *huma.OpenAPI, schema *huma.Schema) (json.RawMessage, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var expand func(any, int) (any, error)
	expand = func(value any, depth int) (any, error) {
		if depth > 30 {
			return nil, ErrInvalid
		}
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				const prefix = "#/components/schemas/"
				if !strings.HasPrefix(ref, prefix) {
					return nil, ErrInvalid
				}
				target := document.Components.Schemas.Map()[strings.TrimPrefix(ref, prefix)]
				if target == nil {
					return nil, ErrInvalid
				}
				bytes, e := json.Marshal(target)
				if e != nil {
					return nil, e
				}
				var replacement any
				if e = json.Unmarshal(bytes, &replacement); e != nil {
					return nil, e
				}
				return expand(replacement, depth+1)
			}
			for key, child := range v {
				expanded, e := expand(child, depth+1)
				if e != nil {
					return nil, e
				}
				v[key] = expanded
			}
			return v, nil
		case []any:
			for i, child := range v {
				expanded, e := expand(child, depth+1)
				if e != nil {
					return nil, e
				}
				v[i] = expanded
			}
			return v, nil
		default:
			return value, nil
		}
	}
	value, err = expand(value, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func (m *Module) registerOperations(api huma.API) error {
	// HTTP may be published to another router after startup (for example, for
	// contract inventory). The immutable manifest is already complete then.
	m.registry.mu.RLock()
	sealed := m.registry.sealed
	m.registry.mu.RUnlock()
	if sealed {
		return nil
	}
	document := api.OpenAPI()
	type descriptor struct {
		id, permission, via string
		human               bool
	}
	wanted := []descriptor{
		{"agent_manifest.get", "", "SELF", false}, {"agent_manifest.versions", "", "SELF", false}, {"agent_capabilities.get", "", "SELF", false},
		{"inbox_events.list", "", "SELF", false}, {"inbox_events.ack", "", "SELF", false},
		{"agent_protocol.live", "agent_workspace_read", "RELATION", false}, {"agent_protocol.reset", "agent_manage", "ROLE", true},
		{"agent_protocol.reset_history", "agent_workspace_read", "RELATION", false},
		{"agent_reminders.list", "", "SELF", false}, {"agent_reminders.create", "", "SELF", false}, {"agent_reminders.change", "", "SELF", false},
		{"agent_operations.list", "", "SELF", false}, {"agent_tools.list", "", "SELF", false}, {"agent_tools.create", "tool_catalog_manage", "ROLE", true}, {"agent_tools.grant", "tool_catalog_manage", "ROLE", true},
	}
	// Collab is mounted before agents in the composed application. Standalone
	// module tests need not register another module's routes.
	for _, item := range document.Paths {
		if item.Post != nil && item.Post.OperationID == "channel.post" {
			wanted = append(wanted, descriptor{"channel.post", "channel_post", "ROLE", false})
			break
		}
	}
	for _, d := range []descriptor{{"work_item_transition.apply", "work_item_write", "ROLE", false}, {"evidence.create", "evidence_submit", "ROLE", false}, {"evidence.update", "evidence_submit", "ROLE", false}, {"defect_from_comment.create", "work_item_write", "ROLE", false}, {"artifact_comment.create", "work_item_write", "ROLE", false}, {"artifact_comment.list", "work_item_read", "ROLE", false}} {
		for _, item := range document.Paths {
			if item.Post != nil && item.Post.OperationID == d.id || item.Get != nil && item.Get.OperationID == d.id || item.Patch != nil && item.Patch.OperationID == d.id {
				wanted = append(wanted, d)
				break
			}
		}
	}
	for _, name := range []string{"detail", "versions", "presets", "publish", "state", "upstream", "metadata", "sources"} {
		id := "agent_tools." + name
		for _, item := range document.Paths {
			found := false
			for _, candidate := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
				if candidate != nil && candidate.OperationID == id {
					found = true
				}
			}
			if found {
				wanted = append(wanted, descriptor{id, "tool_catalog_manage", "ROLE", true})
				break
			}
		}
	}
	for _, d := range wanted {
		var op *huma.Operation
		path, method := "", ""
		for p, item := range document.Paths {
			for verb, candidate := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete} {
				if candidate != nil && candidate.OperationID == d.id {
					op = candidate
					path = p
					method = verb
				}
			}
		}
		if op == nil {
			return ErrInvalid
		}
		input := json.RawMessage(`{"type":"object","additionalProperties":false}`)
		if op.RequestBody != nil {
			if media := op.RequestBody.Content["application/json"]; media != nil {
				var e error
				input, e = inlineSchema(document, media.Schema)
				if e != nil {
					return e
				}
			}
		}
		var output json.RawMessage
		for _, status := range []string{"200", "201", "202"} {
			if response := op.Responses[status]; response != nil {
				if media := response.Content["application/json"]; media != nil {
					var e error
					output, e = inlineSchema(document, media.Schema)
					if e != nil {
						return e
					}
					break
				}
			}
		}
		if output == nil {
			return ErrInvalid
		}
		params := []OperationParameter{}
		for _, p := range op.Parameters {
			schema, e := inlineSchema(document, p.Schema)
			if e != nil {
				return e
			}
			params = append(params, OperationParameter{Name: p.Name, In: p.In, Required: p.Required, Schema: schema})
		}
		writes := method != "GET"
		effects := []string{"READ_ONLY"}
		rule := ""
		if writes {
			effects = []string{"WRITES_STATE"}
			rule = "One key per logical action; reuse it after interrupted with refreshed seen_seq"
		}
		if err := m.registry.Register(Operation{Name: d.id, Summary: op.Summary, Method: method, Path: path, Parameters: params, InputSchema: input, OutputSchema: output, SideEffects: effects, IdempotencyRequired: writes, KeyRule: rule, RequiredPermission: d.permission, Via: d.via, HumanOnly: d.human}); err != nil {
			return err
		}
	}
	return nil
}

func cloneParameters(params []OperationParameter) []OperationParameter {
	out := append([]OperationParameter{}, params...)
	for i := range out {
		out[i].Schema = append(json.RawMessage(nil), out[i].Schema...)
	}
	return out
}
func (m *Module) sealOperations() error {
	for _, op := range m.registry.List() {
		if op.Via != "SELF" {
			if _, ok := m.registrar.Registry().Find(op.RequiredPermission); !ok {
				return ErrInvalid
			}
		}
	}
	m.registry.mu.Lock()
	m.registry.sealed = true
	m.registry.mu.Unlock()
	return nil
}
