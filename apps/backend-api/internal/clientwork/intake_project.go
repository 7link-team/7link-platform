package clientwork

import (
	"7link.com/platform/internal/delivery"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type IntakeProjectConversion struct {
	IntakeConversionBody
	Project delivery.IntakeProjectInput `json:"project"`
}
type IntakeProjectConversionInput struct {
	EditInput[IntakeProjectConversion]
	Cookie string `header:"Cookie"`
}

func (m *Module) registerIntakeProjectHTTP(api huma.API) error {
	iamauthn.RegisterIntakeReauthentication(api)
	path := entityBase + "/intakes/{id}/project"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.new_project", Method: "POST", Path: path, Summary: "Convert an exact Intake card into a new project and draft requirement with fresh Human verification", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "scope", Verb: "change"}, m.convertIntakeProjectHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, path)
}
func (m *Module) convertIntakeProjectHTTP(ctx context.Context, in *IntakeProjectConversionInput) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	if m.writer() == nil || m.intakeProjects.Prepare == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	// Role seeding commits before the business transaction, as required by the
	// canonical project command. It creates no project or conversion effects.
	ids, err := m.intakeProjects.Prepare(ctx, m.writer(), authn.TenantIDFromContext(ctx), in.Body.Project)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.intake.new_project", "POST", entityBase+"/intakes/"+id.String()+"/project", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.convertIntakeProjectOn(ctx, tx, p, id, v, in, ids) })
}
func (m *Module) convertIntakeProjectOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in *IntakeProjectConversionInput, ids []guid.ID) (Receipt, error) {
	if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
		return Receipt{}, err
	}
	if m.intakeSources.SourceOn == nil || m.intakeSources.ConvertProjectOn == nil || m.intakeProjects.CreateOn == nil {
		return Receipt{}, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	source, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, id)
	if err != nil {
		return Receipt{}, err
	}
	if source.Kind != "NEW" || source.State != "TRIAGED" || source.Version != v || source.Triage == nil || source.Triage.Revision != in.Body.CardRevision || source.Triage.Hash != in.Body.ProposalHash {
		return Receipt{}, ErrConflict
	}
	created, err := m.intakeProjects.CreateOn(ctx, tx, tenant, id, in.Body.Project, source.Triage.Draft, ids, in.Cookie, in.Body.ReauthProof)
	if err != nil {
		return Receipt{}, err
	}
	if created.ProjectID <= 0 || created.RequirementID <= 0 || created.ChannelID <= 0 {
		return Receipt{}, ErrUnavailable
	}
	if err = m.intakeSources.ConvertProjectOn(ctx, tx, tenant, p, id, v, in.Body.CardRevision, in.Body.ProposalHash, created.ProjectID, created.RequirementID); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: created.ProjectID, Version: 1, State: "INTAKE"}, nil
}
