package agents

import (
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestManifestIncludesOnlyMountedCommentDefectCommand(t *testing.T) {
	m, _, _, api := agentHTTPHarness(t)
	for _, op := range m.registry.List() {
		require.NotEqual(t, "defect_from_comment.create", op.Name)
	}
	// Rebuild a registry from the composed route document. Standalone agents
	// expose no phantom cross-module operations.
	huma.Register(api, huma.Operation{OperationID: "defect_from_comment.create", Method: "POST", Path: "/test-comment-defects", Summary: "Convert comment", DefaultStatus: 201}, func(ctx context.Context, _ *struct{}) (*respx.Body[string], error) {
		return respx.OK(ctx, "test"), nil
	})
	m.registry = NewOperationRegistry()
	require.NoError(t, m.registerOperations(api))
	var found *Operation
	for _, op := range m.registry.List() {
		if op.Name == "defect_from_comment.create" {
			published := op
			found = &published
		}
	}
	require.NotNil(t, found)
	require.Equal(t, "work_item_write", found.RequiredPermission)
	require.Equal(t, "ROLE", found.Via)
	require.False(t, found.HumanOnly)
	require.True(t, found.IdempotencyRequired)
	require.Equal(t, []string{"WRITES_STATE"}, found.SideEffects)
}

func TestManifestRegistryValidatesAndOwnsAllPublishedBytes(t *testing.T) {
	r := NewOperationRegistry()
	op := Operation{Name: "task.read", Summary: "Read", Method: "GET", Path: "/tasks", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), SideEffects: []string{"READ_ONLY"}, Via: "SELF", Parameters: []OperationParameter{{Name: "id", Schema: json.RawMessage(`{"type":"string"}`)}}}
	for _, mutate := range []func(*Operation){func(o *Operation) { o.Name = "invalid" }, func(o *Operation) { o.InputSchema = json.RawMessage(`null`) }, func(o *Operation) { o.OutputSchema = json.RawMessage(`[]`) }, func(o *Operation) { o.InputSchema = json.RawMessage(`{"type":"string"}`) }, func(o *Operation) { o.Via = "ROLE" }, func(o *Operation) { o.RequiredPermission = "permission" }, func(o *Operation) { o.HumanOnly = true; o.Delegable = true }, func(o *Operation) { o.IdempotencyRequired = true }, func(o *Operation) { o.SideEffects = nil }} {
		bad := op
		mutate(&bad)
		require.ErrorIs(t, r.Register(bad), ErrInvalid)
	}
	require.NoError(t, r.Register(op))
	require.Error(t, r.Register(op))
	op.InputSchema[0] = '!'
	op.Parameters[0].Schema[0] = '!'
	op.SideEffects[0] = "MUTATED"
	first := r.List()
	require.JSONEq(t, `{"type":"object"}`, string(first[0].InputSchema))
	require.JSONEq(t, `{"type":"string"}`, string(first[0].Parameters[0].Schema))
	require.Equal(t, "READ_ONLY", first[0].SideEffects[0])
	first[0].OutputSchema[0] = '!'
	first[0].Parameters[0].Schema[0] = '!'
	first[0].SideEffects[0] = "MUTATED"
	second := r.List()
	require.True(t, json.Valid(second[0].OutputSchema))
	require.True(t, json.Valid(second[0].Parameters[0].Schema))
	require.Equal(t, "READ_ONLY", second[0].SideEffects[0])
}
func TestManifestRejectsUnresolvableAndRecursiveSchemas(t *testing.T) {
	_, _, _, api := agentHTTPHarness(t)
	for _, ref := range []string{"https://example.invalid/schema", "#/components/schemas/Absent"} {
		_, e := inlineSchema(api.OpenAPI(), &huma.Schema{Ref: ref})
		require.ErrorIs(t, e, ErrInvalid)
	}
	registry := api.OpenAPI().Components.Schemas
	schema := &huma.Schema{Ref: "#/components/schemas/Recursive"}
	registry.Map()["Recursive"] = schema
	_, e := inlineSchema(api.OpenAPI(), schema)
	require.ErrorIs(t, e, ErrInvalid)
}
