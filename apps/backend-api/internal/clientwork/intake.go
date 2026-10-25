package clientwork

import (
	"context"
	"encoding/json"
	"reflect"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// The assessed proposal and the external decision have one canonical owner.
// Intake freezes that actual proposal; it never accepts client_approval payloads.
type IntakeChangeBody struct {
	OriginalIntakeID guid.ID `json:"original_intake_id"`
	ChangeID         guid.ID `json:"change_id"`
}
type IntakeConversionBody struct {
	CardRevision int    `json:"triage_card_revision" minimum:"1"`
	ProposalHash string `json:"proposal_hash" pattern:"^[a-f0-9]{64}$"`
	ReauthProof  string `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type IntakeConversionInput struct {
	EditInput[IntakeConversionBody]
	Cookie string `header:"Cookie"`
}

func (m *Module) draftIntakeChangeOn(ctx context.Context, tx *gorm.DB, p guid.ID, in IntakeChangeBody) (Receipt, error) {
	if in.OriginalIntakeID <= 0 || in.ChangeID <= 0 {
		return Receipt{}, ErrInvalid
	}
	if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
		return Receipt{}, e
	}
	if m.intakeSources.DraftOn == nil {
		return Receipt{}, ErrUnavailable
	}
	change, e := m.changeOn(ctx, tx, p, in.ChangeID, false)
	if e != nil {
		return Receipt{}, e
	}
	if (change.State != "AWAITING_CONFIRMATION" && change.State != "CONFIRMED") || change.Proposal == nil {
		return Receipt{}, ErrConflict
	}
	var proposal ClientChangeProposal
	if e = json.Unmarshal([]byte(*change.Proposal), &proposal); e != nil {
		return Receipt{}, e
	}
	if e = m.validateChangeProposalOn(ctx, tx, p, change, &proposal); e != nil {
		return Receipt{}, e
	}
	impact, e := m.WouldBreachOn(ctx, tx, p, proposal.RequirementID, proposal.ToleranceDelta)
	if e != nil {
		return Receipt{}, e
	}
	raw, e := json.Marshal(impact)
	if e != nil {
		return Receipt{}, e
	}
	title := []rune(proposal.Change.Summary)
	if len(title) > 200 {
		title = title[:200]
	}
	source, e := m.intakeSources.DraftOn(ctx, tx, authn.TenantIDFromContext(ctx), p, intake.ChangeSourceDraft{OriginalIntakeID: in.OriginalIntakeID, RequirementID: proposal.RequirementID, ChangeID: change.ID, ProposalHash: change.ProposalHash, ProposalJSON: *change.Proposal, ImpactJSON: string(raw), Title: string(title)})
	if e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: source.ID, Version: source.Version, Revision: source.CardRevision, State: source.State}, nil
}

func (m *Module) convertIntakeChangeOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, cookie string, in IntakeConversionBody) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
		return Receipt{}, e
	}
	if m.intakeSources.ReadOn == nil || m.intakeSources.ConvertedOn == nil || m.intakeAppend.AppendFromIntake == nil {
		return Receipt{}, ErrUnavailable
	}
	tenant := authn.TenantIDFromContext(ctx)
	source, e := m.intakeSources.ReadOn(ctx, tx, tenant, p, id)
	if e != nil {
		return Receipt{}, e
	}
	if source.State != "TRIAGED" || source.Version != v || source.CardRevision != in.CardRevision || source.ProposalHash != in.ProposalHash {
		return Receipt{}, ErrConflict
	}
	change, e := m.changeOn(ctx, tx, p, source.ChangeID, false)
	if e != nil {
		return Receipt{}, e
	}
	if change.State != "CONFIRMED" || change.Proposal == nil || change.ProposalHash != source.ProposalHash || *change.Proposal != source.ProposalJSON {
		return Receipt{}, ErrConflict
	}
	var proposal ClientChangeProposal
	if e = json.Unmarshal([]byte(source.ProposalJSON), &proposal); e != nil {
		return Receipt{}, e
	}
	if proposal.RequirementID != source.RequirementID {
		return Receipt{}, ErrConflict
	}
	var recorded ToleranceChangeSnapshot
	if e = json.Unmarshal([]byte(source.ImpactJSON), &recorded); e != nil {
		return Receipt{}, e
	}
	current, e := m.WouldBreachOn(ctx, tx, p, source.RequirementID, proposal.ToleranceDelta)
	if e != nil {
		return Receipt{}, e
	}
	if !reflect.DeepEqual(recorded.Breached, current.Breached) {
		return Receipt{}, ErrConflict
	}
	if _, e = m.applyChangeWithSourceOn(ctx, tx, p, change.ID, change.Version, cookie, ChangeApplicationBody{ProposalHash: in.ProposalHash, ReauthProof: in.ReauthProof}, id); e != nil {
		return Receipt{}, e
	}
	applied, e := m.changeOn(ctx, tx, p, change.ID, false)
	if e != nil {
		return Receipt{}, e
	}
	if applied.AppliedChangeID == nil {
		return Receipt{}, ErrConflict
	}
	if e = m.intakeSources.ConvertedOn(ctx, tx, tenant, p, id, v, proposal.Change.BaseVersionNo+1, *applied.AppliedChangeID); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: v + 1, Revision: in.CardRevision, State: "CONVERTED"}, nil
}

func (m *Module) registerIntakeHTTP(api huma.API) error {
	if err := m.registerSubmissionHTTP(api); err != nil {
		return err
	}
	if err := m.registerTriageRequestHTTP(api); err != nil {
		return err
	}
	base := entityBase + "/intake-changes"
	owner := authz.ResourceOwnerResolver(m.owner)
	guard := authz.Guard{Resource: "scope", Verb: "change"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.list", Method: "GET", Path: entityBase + "/intakes", Summary: "List internal project Intake processing records", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.listIntakeLedgerHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.source", Method: "GET", Path: entityBase + "/intakes/{id}", Summary: "Read the original Intake source and actual frozen triage card", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getIntakeLedgerHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.triage", Method: "POST", Path: entityBase + "/intakes/{id}/triage-cards", Summary: "Freeze a versioned internal NEW or REWORK Intake assessment", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.triageIntakeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.create", Method: "POST", Path: base, Summary: "Freeze an assessed client change as a genuine Intake source and triage card", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.draftIntakeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.get", Method: "GET", Path: base + "/{id}", Summary: "Read the authorized frozen Intake change source", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, guard, m.getIntakeHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.convert", Method: "POST", Path: base + "/{id}/conversion", Summary: "Append the exact client-confirmed Intake card and adjust baselines atomically", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, guard, m.convertIntakeHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if e := authz.RegisterRequestContract(api, entityBase+"/intakes/{id}/triage-cards"); e != nil {
		return e
	}
	if e := authz.RegisterRequestContract(api, base); e != nil {
		return e
	}
	return authz.RegisterRequestContract(api, base+"/{id}/conversion")
}

func (m *Module) triageIntakeHTTP(ctx context.Context, in *EditInput[intake.TriageDraft]) (*CommandOutput, error) {
	p, id, version, err := editIDs(in.EntityID, in.ID, in.IfMatch)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return m.command(ctx, p, "clientwork.intake.triage", "POST", entityBase+"/intakes/"+id.String()+"/triage-cards", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.intakeSources.TriageOn == nil {
			return Receipt{}, ErrUnavailable
		}
		card, err := m.intakeSources.TriageOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id, version, in.Body)
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: id, Version: card.Version, Revision: card.Revision, State: "TRIAGED"}, nil
	})
}

func (m *Module) listIntakeLedgerHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[intake.IntakeLedgerEntry]], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.ListOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	out := Page[intake.IntakeLedgerEntry]{Items: []intake.IntakeLedgerEntry{}}
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
			return e
		}
		scope := cursorScope(ctx, p, "intakes")
		last, e := position(in.Cursor, scope)
		if e != nil {
			return e
		}
		page, e := m.intakeSources.ListOn(ctx, tx, authn.TenantIDFromContext(ctx), p, last, in.Limit)
		if e != nil {
			return e
		}
		out.Items, out.HasMore = page.Items, page.HasMore
		if page.HasMore && len(page.Items) > 0 {
			out.NextCursor = cursor(scope, page.Items[len(page.Items)-1].ID)
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) getIntakeLedgerHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.IntakeLedgerSource], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.SourceOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.IntakeLedgerSource
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
			return e
		}
		var e error
		out, e = m.intakeSources.SourceOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) draftIntakeHTTP(ctx context.Context, in *CreateInput[IntakeChangeBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.intake.create", "POST", entityBase+"/intake-changes", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.draftIntakeChangeOn(ctx, tx, p, in.Body) })
}
func (m *Module) convertIntakeHTTP(ctx context.Context, in *IntakeConversionInput) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.intake.convert", "POST", entityBase+"/intake-changes/"+id.String()+"/conversion", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) {
		return m.convertIntakeChangeOn(ctx, tx, p, id, v, in.Cookie, in.Body)
	})
}
func (m *Module) getIntakeHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.ChangeSourceView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.intakeSources.ReadOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.ChangeSourceView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
			return e
		}
		var e error
		out, e = m.intakeSources.ReadOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id)
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
