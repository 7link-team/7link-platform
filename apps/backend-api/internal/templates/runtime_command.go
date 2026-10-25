package templates

import (
	"context"
	"slices"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type gateInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		ExpansionKey string `json:"expansion_key" minLength:"1" maxLength:"512"`
		Hash         string `json:"hash" minLength:"64" maxLength:"64"`
		Outcome      string `json:"outcome" enum:"APPROVE,REJECT"`
		Reason       string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}

func (m *Module) registerWorkflowCommands(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.gate-decision", Method: "POST", Path: "/projects/{project_id}/workflow/gate-decisions", Summary: "Record an immutable Human gate decision for an exact workflow expansion", Tags: []string{"projects"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "template", Verb: "read", FilterData: true}, m.gateHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "template.brief-sign", Method: "POST", Path: "/projects/{project_id}/workflow/signed-briefs", Summary: "Sign an immutable requirement confirmation sheet for workflow fanout", Tags: []string{"projects"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "template", Verb: "read", FilterData: true}, m.signBriefHTTP)
	return nil
}
func (m *Module) recordGateOn(ctx context.Context, tx *gorm.DB, project guid.ID, in *gateInput) error {
	row, runs, _, e := m.authorizeInstanceCommandOn(ctx, tx, project)
	if e != nil {
		return e
	}
	if row.SHA256 != in.Body.Hash {
		return ErrConflict
	}
	i := slices.IndexFunc(runs, func(r TemplateNodeRun) bool { return r.ExpansionKey == in.Body.ExpansionKey })
	if i < 0 {
		return ErrNotFound
	}
	r := runs[i]
	if r.Gate == "" || r.State == "SKIPPED" || (r.State != "COMPLETED" && r.Reason != "MISSING_GATE_DECISION" && r.Reason != "GATE_REJECTED") {
		return ErrConflict
	}
	if m.gates.RecordOn == nil {
		return ErrUnavailable
	}
	_, e = m.gates.RecordOn(ctx, tx, governance.WorkflowDecisionInput{WorkflowDecisionRef: governance.WorkflowDecisionRef{TenantID: row.TenantID, ProjectID: project, BindingID: row.ID, DefinitionHash: row.SHA256, ExpansionKey: r.ExpansionKey, Gate: r.Gate}, Outcome: in.Body.Outcome, Reason: in.Body.Reason})
	return e
}
func (m *Module) gateHTTP(ctx context.Context, in *gateInput) (*respx.Body[TemplateInstanceView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.gate:"+in.ProjectID, in.Body, func(tx *gorm.DB) (TemplateInstanceView, error) { return m.advanceOn(ctx, tx, project) }, func(tx *gorm.DB) error { return m.recordGateOn(ctx, tx, project, in) })
}

type signBriefInput struct {
	ProjectID      string `path:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Hash     string             `json:"hash" minLength:"64" maxLength:"64"`
		IntakeID guid.ID            `json:"intake_id"`
		Items    []intake.BriefItem `json:"items" minItems:"1" maxItems:"1000"`
	}
}

func (m *Module) signBriefHTTP(ctx context.Context, in *signBriefInput) (*respx.Body[TemplateInstanceView], error) {
	project, e := guid.RequireID(in.ProjectID)
	if e != nil {
		return nil, templateError(ctx, ErrInvalid)
	}
	return command(m, ctx, in.IdempotencyKey, "template.brief-sign:"+in.ProjectID, in.Body, func(tx *gorm.DB) (TemplateInstanceView, error) { return m.advanceOn(ctx, tx, project) }, func(tx *gorm.DB) error {
		row, _, _, e := m.authorizeInstanceCommandOn(ctx, tx, project)
		if e != nil {
			return e
		}
		if row.SHA256 != in.Body.Hash {
			return ErrConflict
		}
		if m.briefs.SignOn == nil {
			return ErrUnavailable
		}
		_, e = m.briefs.SignOn(ctx, tx, intake.SignedBriefInput{TenantID: row.TenantID, ProjectID: project, BindingID: row.ID, IntakeID: in.Body.IntakeID, Items: in.Body.Items})
		return e
	})
}
