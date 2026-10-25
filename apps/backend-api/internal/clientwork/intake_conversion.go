package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type IntakeNewConversion struct {
	CardRevision int    `json:"triage_card_revision" minimum:"1"`
	ProposalHash string `json:"proposal_hash" pattern:"^[a-f0-9]{64}$"`
}

func (m *Module) convertNewIntakeOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, in IntakeNewConversion) (Receipt, error) {
	if _, err := m.authorize(ctx, tx, p, "scope_change", false); err != nil {
		return Receipt{}, err
	}
	if m.intakeSources.SourceOn == nil || m.intakeSources.ConvertNewOn == nil || m.intakeAppend.NewRequirementOn == nil {
		return Receipt{}, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	source, err := m.intakeSources.SourceOn(ctx, tx, tenant, p, id)
	if err != nil {
		return Receipt{}, err
	}
	if source.Kind != "NEW" || source.State != "TRIAGED" || source.Version != v || source.Triage == nil || source.Triage.Revision != in.CardRevision || source.Triage.Hash != in.ProposalHash {
		return Receipt{}, ErrConflict
	}
	requirement, err := m.intakeAppend.NewRequirementOn(ctx, tx, tenant, p, id, source.Triage.Draft)
	if err != nil {
		return Receipt{}, err
	}
	if err = m.intakeSources.ConvertNewOn(ctx, tx, tenant, p, id, v, in.CardRevision, in.ProposalHash, requirement); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: requirement, Version: 1, State: "DRAFT"}, nil
}

func (m *Module) registerNewIntakeHTTP(api huma.API) error {
	delegatedPath := entityBase + "/intakes/{id}/delegated-requirement"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.delegated_card", Method: "GET", Path: delegatedPath, Summary: "Read the frozen NEW card under current Agent conversion authority", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "intake", Verb: "convert"}, m.getDelegatedNewCardHTTP)

	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.delegated_requirement", Method: "POST", Path: delegatedPath, Summary: "Convert an exact existing-project NEW card under current Agent delegation", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "intake", Verb: "convert"}, m.convertDelegatedNewHTTP)
	if !m.registrar.IsDeclarationOnly() {
		if err := authz.RegisterRequestContract(api, delegatedPath); err != nil {
			return err
		}
	}
	path := entityBase + "/intakes/{id}/requirement"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.new_requirement", Method: "POST", Path: path, Summary: "Convert the exact NEW Intake card into a draft requirement", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "scope", Verb: "change"}, m.convertNewIntakeHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) convertNewIntakeHTTP(ctx context.Context, in *EditInput[IntakeNewConversion]) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.intake.new_requirement", "POST", entityBase+"/intakes/"+id.String()+"/requirement", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.convertNewIntakeOn(ctx, tx, p, id, v, in.Body) })
}

// A distinct request type selects the narrow authority path before replay.
// It never enables Agent access for an ordinary Clientwork command.
type DelegatedNewInput struct{ EditInput[IntakeNewConversion] }

func (m *Module) convertDelegatedNewHTTP(ctx context.Context, in *DelegatedNewInput) (*CommandOutput, error) {
	p, id, v, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.intake.delegated_requirement", "POST", entityBase+"/intakes/"+id.String()+"/delegated-requirement", in.Key, 201, in, "intake_convert", false, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.delegatedConversion.CreateOn == nil {
			return Receipt{}, ErrUnavailable
		}
		requirement, err := m.delegatedConversion.CreateOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id, v, in.Body.CardRevision, in.Body.ProposalHash)
		return Receipt{ID: requirement, Version: 1, State: "DRAFT"}, err
	})
}

type DelegatedNewCardInput struct {
	EntityID string `path:"entity_id"`
	ID       string `path:"id"`
}

func (m *Module) getDelegatedNewCardHTTP(ctx context.Context, in *DelegatedNewCardInput) (*respx.Body[intake.TriageSource], error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.delegatedConversion.ReadOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var card intake.TriageSource
	err = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = m.delegatedConversion.ReadOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, card), nil
}
