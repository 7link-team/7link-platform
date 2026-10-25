package templates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowModelProposalActualTransportReviewReplayAndRollback(t *testing.T) {
	for _, fault := range []string{"", "model", "digest", "base", "offline", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			providerFixture(t, m)
			var v TemplateVersionView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				v, e = m.draftOn(ctx, tx, "assistant", testDefinition())
				return e
			}))
			calls := 0
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					_, _ = io.WriteString(w, `{"protocol":"provider-v1","available":true}`)
					return
				}
				if !assert.Equal(t, "/workflow-proposals", r.URL.Path) {
					return
				}
				if !assert.Equal(t, "Bearer model-private", r.Header.Get("Authorization")) {
					return
				}
				if fault == "offline" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				var in workflowModelRequest
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&in)) {
					return
				}
				calls++
				if !assert.Equal(t, "configured-model", in.Model) {
					return
				}
				if !assert.Equal(t, v.Hash, in.BaseHash) {
					return
				}
				if !assert.Equal(t, "Rename the implementation step", in.Instruction) {
					return
				}
				node := in.Definition.Workflow.Nodes[0]
				node.Name = "Model proposed implementation"
				response := workflowModelResponse{Protocol: in.Protocol, Model: in.Model, RequestSHA256: in.RequestSHA256, Patch: TemplateWorkflowPatch{BaseHash: in.BaseHash, BaseVersion: in.Definition.Workflow.Version, Reason: "Human requested rename", Operations: []TemplatePatchOperation{{Kind: "update_node", ID: node.ID, Node: &node}}}}
				if fault == "model" {
					response.Model = "another-model"
				}
				if fault == "digest" {
					response.RequestSHA256 = strings.Repeat("0", 64)
				}
				if fault == "base" {
					response.Patch.BaseHash = strings.Repeat("0", 64)
				}
				if !assert.NoError(t, json.NewEncoder(w).Encode(response)) {
					return
				}
			}))
			defer service.Close()
			endpoint, _ := json.Marshal(service.URL)
			var provider ProviderView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				provider, e = m.createProviderOn(ctx, tx, ProviderDraft{Kind: "LLM_JUDGE", AdapterKey: "llm_judge_http_v1", DisplayName: "Model", Config: map[string]json.RawMessage{"endpoint": endpoint, "model": json.RawMessage(`"configured-model"`)}, Secret: "model-private", AcceptLicenseNotice: true})
				return e
			}))
			_, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "health"})
			require.NoError(t, e)
			if fault == "outbox" {
				m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				})
			}
			in := &workflowProposalInput{ID: v.ID.String(), IdempotencyKey: "proposal", Body: WorkflowProposalDraft{ProviderID: provider.ID, BaseHash: v.Hash, Instruction: "Rename the implementation step"}}
			out, e := m.workflowProposalHTTP(ctx, in)
			if fault != "" {
				require.Error(t, e)
				if fault == "outbox" {
					m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 1, nil })
					recovered, e := m.workflowProposalHTTP(ctx, in)
					require.NoError(t, e)
					require.True(t, recovered.Body.Data.Validation.Valid)
					require.Equal(t, 2, calls, "failed audit/outbox transaction must not retain a replay receipt")
				}
			} else {
				require.NoError(t, e)
				require.True(t, out.Body.Data.Validation.Valid)
				require.Equal(t, "Model proposed implementation", out.Body.Data.Workflow.Nodes[0].Name)
				require.NotEmpty(t, out.Body.Data.ResponseSHA256)
				replay, e := m.workflowProposalHTTP(ctx, in)
				require.NoError(t, e)
				require.Equal(t, out.Body.Data, replay.Body.Data)
				require.Equal(t, 1, calls)
				m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
				_, e = m.workflowProposalHTTP(ctx, in)
				assertStatus(t, e, 403)
			}
			row, e := query.BindVersions(db, m.names).WithContext(ctx).Where(query.BindVersions(db, m.names).ID.Eq(v.ID)).First()
			require.NoError(t, e)
			unchanged, e := versionView(row)
			require.NoError(t, e)
			require.Equal(t, v, unchanged, "model generation never applies or publishes a draft")
		})
	}
}

func TestWorkflowServerDiagnosticsResolveChildACLAndInheritedGatePaths(t *testing.T) {
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Workflow.Nodes[1].Mandatory = true
	var original TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		original, e = m.draftOn(ctx, tx, "source-diagnostics", d)
		if e != nil {
			return e
		}
		original, e = m.publishOn(ctx, tx, original.ID, original.Hash)
		return e
	}))
	input := &draftInput{IdempotencyKey: "copy-diagnostics"}
	input.Body.Key = "copy-diagnostics"
	input.Body.Name = "Diagnostic copy"
	input.Body.BasedOnVersionID = original.ID
	snapshot, e := m.draftHTTP(ctx, input)
	require.NoError(t, e)
	candidate := cloneDefinition(snapshot.Body.Data.Definition).Workflow
	candidate.Nodes[1].Mandatory = false
	candidate.Nodes = append(candidate.Nodes, TemplateNode{ID: "child", Name: "Child", Kind: "SUBFLOW", Subflow: &TemplateWorkflowRef{WorkflowID: "private-child", Version: 7}})
	in := &workflowValidationInput{ID: snapshot.Body.Data.ID.String()}
	in.Body.BaseHash = snapshot.Body.Data.Hash
	in.Body.Workflow = candidate
	out, e := m.workflowValidateHTTP(ctx, in)
	require.NoError(t, e)
	require.False(t, out.Body.Data.Valid)
	require.Contains(t, out.Body.Data.Issues, WorkflowDiagnostic{"workflow.nodes[3].subflow", "templateIssueSubflow"})
	require.Contains(t, out.Body.Data.Issues, WorkflowDiagnostic{"workflow.inherited_gates[" + d.Workflow.Nodes[1].ID + "]", "templateIssueInherited"})
	in.Body.Workflow = snapshot.Body.Data.Definition.Workflow
	out, e = m.workflowValidateHTTP(ctx, in)
	require.NoError(t, e)
	require.True(t, out.Body.Data.Valid)
	in.Body.BaseHash = strings.Repeat("0", 64)
	_, e = m.workflowValidateHTTP(ctx, in)
	assertStatus(t, e, 409)
}

func TestWorkflowDiagnosticsHTTPWire(t *testing.T) {
	m, db, ctx := templateHarness(t)
	var v TemplateVersionView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.draftOn(ctx, tx, "wire-validation", testDefinition())
		return e
	}))
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "validation-wire", Method: "POST", Path: "/templates/{template_id}/workflow-validations"}, func(_ context.Context, in *workflowValidationInput) (*respx.Body[WorkflowValidation], error) {
		return m.workflowValidateHTTP(ctx, in)
	})
	url := "/templates/" + v.ID.String() + "/workflow-validations"
	good := map[string]any{"base_hash": v.Hash, "workflow": v.Definition.Workflow}
	response := api.Post(url, good)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"valid":true`)
	require.Equal(t, 422, api.Post(url, map[string]any{"base_hash": "bad"}).Code)
}

func TestWorkflowAssistantRejectsStaleOrUnavailableDefinitionAndProvider(t *testing.T) {
	m, db, ctx := templateHarness(t)
	v := draftTemplate(t, m, db, ctx, "assistant-boundaries")
	invalid := &workflowValidationInput{ID: "bad"}
	_, e := m.workflowValidateHTTP(ctx, invalid)
	assertStatus(t, e, 422)
	input := &workflowValidationInput{ID: v.ID.String()}
	input.Body.BaseHash = "stale"
	input.Body.Workflow = v.Definition.Workflow
	_, e = m.workflowValidateHTTP(ctx, input)
	assertStatus(t, e, 409)
	proposal := &workflowProposalInput{ID: "bad"}
	_, e = m.workflowProposalHTTP(ctx, proposal)
	assertStatus(t, e, 422)
	proposal = &workflowProposalInput{ID: v.ID.String(), IdempotencyKey: "invalid-instruction", Body: WorkflowProposalDraft{BaseHash: v.Hash, ProviderID: 999}}
	_, e = m.workflowProposalHTTP(ctx, proposal)
	assertStatus(t, e, 422)
	proposal.Body.Instruction = "Propose edit"
	proposal.IdempotencyKey = "missing-provider"
	_, e = m.workflowProposalHTTP(ctx, proposal)
	assertStatus(t, e, 422)
	injected := errors.New("definition storage unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workflow-read-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Versions.String() {
			tx.AddError(injected)
		}
	}))
	input.Body.BaseHash = v.Hash
	_, e = m.workflowValidateHTTP(ctx, input)
	assertStatus(t, e, 500)
	require.NoError(t, db.Callback().Query().Remove("workflow-read-failure"))
}

func TestWorkflowDiagnosticsTraverseAuthorizedChildrenAndBoundGraph(t *testing.T) {
	m, db, ctx := templateHarness(t)
	child := testDefinition()
	child.Workflow.WorkflowID = "diagnostic-child"
	publishRuntime(t, m, db, ctx, "diagnostic-child", child)
	v := draftTemplate(t, m, db, ctx, "diagnostic-parent")
	input := &workflowValidationInput{ID: v.ID.String()}
	input.Body.BaseHash = v.Hash
	input.Body.Workflow = v.Definition.Workflow
	input.Body.Workflow.Nodes = append(input.Body.Workflow.Nodes, TemplateNode{ID: "sub", Name: "Child", Kind: "SUBFLOW", Subflow: &TemplateWorkflowRef{WorkflowID: "diagnostic-child", Version: 1}})
	out, e := m.workflowValidateHTTP(ctx, input)
	require.NoError(t, e)
	require.NotContains(t, out.Body.Data.Issues, WorkflowDiagnostic{"workflow.nodes[3].subflow", "templateIssueSubflow"})
	input.Body.Workflow.Nodes[3].Subflow = nil
	out, e = m.workflowValidateHTTP(ctx, input)
	require.NoError(t, e)
	require.Contains(t, out.Body.Data.Issues, WorkflowDiagnostic{"workflow.nodes[3].subflow", "templateIssueSubflow"})
	input.Body.Workflow.Nodes = make([]TemplateNode, 1001)
	out, e = m.workflowValidateHTTP(ctx, input)
	require.NoError(t, e)
	require.False(t, out.Body.Data.Valid)
	require.Contains(t, out.Body.Data.Issues, WorkflowDiagnostic{"workflow", "templateIssueGraph"})
}
