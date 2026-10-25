package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorkflowDiagnostic struct {
	Path string `json:"path"`
	Code string `json:"code" enum:"templateIssueGraph,templateIssueSubflow,templateIssueInherited"`
}
type WorkflowValidation struct {
	Valid  bool                 `json:"valid"`
	Issues []WorkflowDiagnostic `json:"issues"`
}
type workflowValidationInput struct {
	ID   string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body struct {
		BaseHash string              `json:"base_hash" minLength:"64" maxLength:"64"`
		Workflow TemplateWorkflowDef `json:"workflow"`
	}
}

// This is the same authorized child/source validation used by save/publication,
// applied to a detached local candidate. Child details unavailable to the Human
// are reported only as unresolved references, never disclosed from another tenant.
func (m *Module) workflowDiagnosticsOn(ctx context.Context, tx *gorm.DB, row *model.Version, d TemplateDefinition) (WorkflowValidation, error) {
	out := WorkflowValidation{Issues: []WorkflowDiagnostic{}}
	children, e := m.childrenOn(ctx, tx)
	if e != nil {
		return out, e
	}
	visited := 0
	var visit func(TemplateWorkflowDef, string, map[TemplateWorkflowRef]bool)
	visit = func(w TemplateWorkflowDef, path string, seen map[TemplateWorkflowRef]bool) {
		for i, n := range w.Nodes {
			visited++
			if visited > 1000 {
				out.Issues = append(out.Issues, WorkflowDiagnostic{path, "templateIssueGraph"})
				return
			}
			if n.Kind != "SUBFLOW" {
				continue
			}
			p := fmt.Sprintf("%s.nodes[%d].subflow", path, i)
			if n.Subflow == nil {
				out.Issues = append(out.Issues, WorkflowDiagnostic{p, "templateIssueSubflow"})
				continue
			}
			child, ok := children[*n.Subflow]
			if !ok || seen[*n.Subflow] {
				out.Issues = append(out.Issues, WorkflowDiagnostic{p, "templateIssueSubflow"})
				continue
			}
			seen[*n.Subflow] = true
			visit(child, p, seen)
			delete(seen, *n.Subflow)
		}
	}
	visit(d.Workflow, "workflow", map[TemplateWorkflowRef]bool{{d.Workflow.WorkflowID, d.Workflow.Version}: true})
	gates, e := m.inheritedMandatoryGatesOn(ctx, tx, row)
	if e != nil {
		return out, e
	}
	for _, g := range gates {
		if !g.Mandatory {
			continue
		}
		found := false
		for _, n := range d.Workflow.Nodes {
			if n.ID == g.ID && n.Kind == "HUMAN_GATE" && n.Mandatory && n.Gate == g.Gate {
				found = true
				break
			}
		}
		if !found {
			out.Issues = append(out.Issues, WorkflowDiagnostic{"workflow.inherited_gates[" + g.ID + "]", "templateIssueInherited"})
		}
	}
	if e = d.Validate(children); e != nil {
		out.Issues = append(out.Issues, WorkflowDiagnostic{"workflow", "templateIssueGraph"})
	}
	out.Valid = len(out.Issues) == 0
	return out, nil
}
func (m *Module) workflowValidateHTTP(ctx context.Context, in *workflowValidationInput) (*respx.Body[WorkflowValidation], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	var out WorkflowValidation
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		r, e := m.versionOn(ctx, tx, id, "template_manage")
		if e != nil {
			return e
		}
		if r.SHA256 != in.Body.BaseHash {
			return ErrConflict
		}
		v, e := versionView(r)
		if e != nil {
			return e
		}
		v.Definition.Workflow = in.Body.Workflow
		out, e = m.workflowDiagnosticsOn(ctx, tx, r, v.Definition)
		return e
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

type WorkflowProposalDraft struct {
	ProviderID  guid.ID `json:"provider_id"`
	BaseHash    string  `json:"base_hash" minLength:"64" maxLength:"64"`
	Instruction string  `json:"instruction" minLength:"1" maxLength:"8000"`
}
type workflowProposalInput struct {
	ID             string `path:"template_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           WorkflowProposalDraft
}
type WorkflowProposal struct {
	Patch           TemplateWorkflowPatch `json:"patch"`
	Workflow        TemplateWorkflowDef   `json:"workflow"`
	Validation      WorkflowValidation    `json:"validation"`
	ProviderID      guid.ID               `json:"provider_id"`
	ProviderVersion int                   `json:"provider_version"`
	Model           string                `json:"model"`
	RequestSHA256   string                `json:"request_sha256"`
	ResponseSHA256  string                `json:"response_sha256"`
}
type workflowModelRequest struct {
	Protocol        string             `json:"protocol"`
	IdempotencyKey  string             `json:"idempotency_key"`
	ProviderID      guid.ID            `json:"provider_id"`
	ProviderVersion int                `json:"provider_version"`
	Model           string             `json:"model"`
	BaseHash        string             `json:"base_hash"`
	Definition      TemplateDefinition `json:"definition"`
	Instruction     string             `json:"instruction"`
	RequestSHA256   string             `json:"request_sha256"`
}
type workflowModelResponse struct {
	Protocol      string                `json:"protocol"`
	Model         string                `json:"model"`
	RequestSHA256 string                `json:"request_sha256"`
	Patch         TemplateWorkflowPatch `json:"patch"`
}

func (m *Module) workflowProposalHTTP(ctx context.Context, in *workflowProposalInput) (*respx.Body[WorkflowProposal], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.workflow-proposal:"+in.ID, in.Body, func(tx *gorm.DB) (WorkflowProposal, error) {
		out := WorkflowProposal{}
		r, e := m.versionOn(ctx, tx, id, "template_manage")
		if e != nil {
			return out, e
		}
		if r.State != "DRAFT" || r.SHA256 != in.Body.BaseHash {
			return out, ErrConflict
		}
		if strings.TrimSpace(in.Body.Instruction) == "" || len(in.Body.Instruction) > 8000 || in.Body.ProviderID <= 0 {
			return out, ErrInvalid
		}
		v, e := versionView(r)
		if e != nil {
			return out, e
		}
		q := query.BindProviders(tx, m.names)
		p, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(in.Body.ProviderID), q.TenantID.Eq(r.TenantID)).First()
		if e != nil {
			return out, ErrProviderAdapter
		}
		if p.AdapterKey != "llm_judge_http_v1" || p.Kind != "LLM_JUDGE" || p.HealthCheckedAt == nil || m.providerView(p).State != "ENABLED" {
			return out, ErrProviderAdapter
		}
		conn, cleanup, e := m.providerConnection(p)
		if e != nil {
			return out, e
		}
		defer cleanup()
		cfg, e := parseVerifierHTTPConfig(conn.Config)
		if e != nil || strings.TrimSpace(cfg.Model) == "" {
			return out, ErrInvalid
		}
		req := workflowModelRequest{Protocol: "provider-v1", IdempotencyKey: "workflow-proposal:" + authn.PrincipalIDFromContext(ctx).String() + ":" + id.String() + ":" + in.IdempotencyKey, ProviderID: p.ID, ProviderVersion: p.Version, Model: cfg.Model, BaseHash: r.SHA256, Definition: v.Definition, Instruction: in.Body.Instruction}
		raw, e := json.Marshal(req)
		if e != nil {
			return out, e
		}
		req.RequestSHA256 = evalDigest(raw)
		work, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var response workflowModelResponse
		if e = verifierHTTPCall(work, conn, "POST", "/workflow-proposals", req, &response); e != nil {
			return out, e
		}
		if response.Protocol != req.Protocol || response.Model != cfg.Model || response.RequestSHA256 != req.RequestSHA256 {
			return out, ErrInvalid
		}
		children, e := m.childrenOn(ctx, tx)
		if e != nil {
			return out, e
		}
		candidate, e := ApplyPatch(v.Definition, response.Patch, children)
		if e != nil {
			return out, e
		}
		validation, e := m.workflowDiagnosticsOn(ctx, tx, r, candidate)
		if e != nil {
			return out, e
		}
		raw, e = json.Marshal(response)
		if e != nil {
			return out, e
		}
		for _, secret := range conn.Environment {
			if secret != "" && strings.Contains(string(raw), secret) {
				return out, ErrInvalid
			}
		}
		if _, e = m.versionOn(ctx, tx, id, "template_manage"); e != nil {
			return out, e
		}
		out = WorkflowProposal{Patch: response.Patch, Workflow: candidate.Workflow, Validation: validation, ProviderID: p.ID, ProviderVersion: p.Version, Model: cfg.Model, RequestSHA256: req.RequestSHA256, ResponseSHA256: evalDigest(raw)}
		a := audit.NewDomainEvent(ctx, r.TenantID, "template.workflow_proposed", "template_version", id)
		a.Detail["authority_source"] = "IAM:template_manage"
		a.Detail["provider_id"], a.Detail["provider_version"], a.Detail["request_sha256"], a.Detail["response_sha256"] = p.ID, p.Version, out.RequestSHA256, out.ResponseSHA256
		if e = m.audit(ctx, tx, a); e != nil {
			return out, e
		}
		_, e = m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: "template.workflow_proposed", Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:template_manage"}, Subject: events.Subject{Kind: "template_version", ID: id}, SubjectVersion: int64(r.Version), Data: map[string]any{"base_hash": r.SHA256, "provider_id": p.ID, "request_sha256": out.RequestSHA256, "response_sha256": out.ResponseSHA256}})
		return out, e
	}, func(tx *gorm.DB) error { _, e := m.versionOn(ctx, tx, id, "template_manage"); return e })
}
