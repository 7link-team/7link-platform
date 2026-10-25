package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ClientChangeProposal struct {
	ToleranceDelta ApprovedChangeDelta `json:"tolerance_delta,omitempty"`

	RequirementID      guid.ID           `json:"requirement_id"`
	RequirementVersion int               `json:"requirement_version" minimum:"1"`
	Change             ClientChangeDraft `json:"change"`
}

// Drafts contain no step-up credential; a fresh proof belongs only to application.
type ClientChangeDraft struct {
	Criteria      delivery.ClientScopeCriteria `json:"criteria,omitempty"`
	BaseVersionNo int                          `json:"base_version_no" minimum:"1"`
	Summary       string                       `json:"summary" minLength:"5" maxLength:"2000"`
	Title         *string                      `json:"title,omitempty" minLength:"1" maxLength:"200"`
	Description   *string                      `json:"description,omitempty" maxLength:"20000"`
	Priority      *int                         `json:"priority,omitempty" minimum:"0" maximum:"3"`
	StaleWorkIDs  []guid.ID                    `json:"stale_work_ids,omitempty" maxItems:"500"`
}

// The external view excludes affected internal work IDs and all credentials.
type ClientChangePublic struct {
	ToleranceDelta ApprovedChangeDelta `json:"tolerance_delta,omitempty"`

	Criteria      delivery.ClientScopeCriteria `json:"criteria"`
	RequirementID guid.ID                      `json:"requirement_id"`
	BaseVersion   int                          `json:"base_version"`
	Title         *string                      `json:"title,omitempty"`
	Description   *string                      `json:"description,omitempty"`
	Priority      *int                         `json:"priority,omitempty"`
}
type ChangeApplicationBody struct {
	ProposalHash string `json:"proposal_hash" pattern:"^[a-f0-9]{64}$"`
	ReauthProof  string `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type ChangeApplicationInput struct {
	EditInput[ChangeApplicationBody]
	Cookie string `header:"Cookie"`
}

func (m *Module) validateChangeProposalOn(ctx context.Context, tx *gorm.DB, p guid.ID, row *model.ChangeRequest, in *ClientChangeProposal) error {
	if !validDelta(in.ToleranceDelta) || in.RequirementID <= 0 || in.RequirementVersion < 1 || in.Change.BaseVersionNo < 1 || !validText(in.Change.Summary, 2000) || (in.Change.Title == nil && in.Change.Description == nil && in.Change.Priority == nil && len(in.Change.Criteria.Add) == 0 && len(in.Change.Criteria.Retire) == 0 && len(in.Change.Criteria.Supersede) == 0) {
		return ErrInvalid
	}
	if len(in.Change.StaleWorkIDs) > 500 || len(in.Change.Criteria.Add) > 50 || len(in.Change.Criteria.Retire) > 50 || len(in.Change.Criteria.Supersede) > 50 {
		return ErrInvalid
	}
	sheet, e := m.loadSheet(ctx, tx, p, row.SheetID)
	if e != nil {
		return e
	}
	snapshot, e := m.sheetView(ctx, tx, sheet, false, row.BaseRevision)
	if e != nil {
		return e
	}
	found := false
	for _, r := range snapshot.Content.Items {
		if r.ID == in.RequirementID {
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	current, e := m.projects.RequirementsOn(ctx, tx, row.TenantID, p, []guid.ID{in.RequirementID})
	if e != nil {
		return e
	}
	if len(current) != 1 || current[0].Version != in.RequirementVersion {
		return ErrConflict
	}
	return nil
}
func (m *Module) registerChangeApplicationHTTP(api huma.API) error {
	path := entityBase + "/client-changes/{id}/applications"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.change.apply", Method: "POST", Path: path, Summary: "Apply the exact client-confirmed proposal through the canonical scope command", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "scope", Verb: "change"}, m.applyChangeHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, path)
}
func (m *Module) applyChangeHTTP(ctx context.Context, in *ChangeApplicationInput) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, p, "clientwork.change.apply", "POST", entityBase+"/client-changes/"+id.String()+"/applications", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.applyChangeOn(ctx, tx, p, id, v, in.Cookie, in.Body) })
}
func (m *Module) applyChangeOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, cookie string, in ChangeApplicationBody) (Receipt, error) {
	return m.applyChangeWithSourceOn(ctx, tx, p, id, v, cookie, in, 0)
}
func (m *Module) applyChangeWithSourceOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, v int, cookie string, in ChangeApplicationBody, source guid.ID) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, p, "scope_change", false); e != nil {
		return Receipt{}, e
	}
	row, e := m.changeOn(ctx, tx, p, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.State != "CONFIRMED" || row.Version != v || row.Proposal == nil || row.ProposalHash != in.ProposalHash {
		return Receipt{}, ErrConflict
	}
	var proposal ClientChangeProposal
	if e = json.Unmarshal([]byte(*row.Proposal), &proposal); e != nil {
		return Receipt{}, e
	}
	draft := proposal.Change
	command := delivery.ClientScopeChange{Criteria: draft.Criteria, BaseVersionNo: draft.BaseVersionNo, Summary: draft.Summary, Title: draft.Title, Description: draft.Description, Priority: draft.Priority, StaleWorkIDs: draft.StaleWorkIDs, ReauthProof: in.ReauthProof}
	var change guid.ID
	if source > 0 {
		if m.intakeAppend.AppendFromIntake == nil {
			return Receipt{}, ErrUnavailable
		}
		change, e = m.intakeAppend.AppendFromIntake(ctx, tx, row.TenantID, p, proposal.RequirementID, source, proposal.RequirementVersion, cookie, command)
	} else {
		change, e = m.projects.ApplyClientChangeOn(ctx, tx, row.TenantID, p, proposal.RequirementID, proposal.RequirementVersion, cookie, command)
	}
	if e != nil {
		return Receipt{}, e
	}
	toleranceSource := id
	sourceField := "source_change_id"
	if source > 0 {
		toleranceSource = source
		sourceField = "source_intake_id"
	}
	if e = m.applyApprovedToleranceChangeOn(ctx, tx, p, proposal.RequirementID, toleranceSource, proposal.ToleranceDelta, sourceField); e != nil {
		return Receipt{}, e
	}
	row.State = "APPLIED"
	row.AppliedChangeID = &change
	row.Version++
	row.UpdatedAt = now()
	q := query.BindChangeRequest(tx, m.names)
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.State, q.AppliedChangeID, q.Version, q.UpdatedAt).Updates(row); e != nil {
		return Receipt{}, e
	}
	if e = m.event(ctx, tx, p, id, "change_decided", "scope_change", row.Version); e != nil {
		return Receipt{}, e
	}
	return Receipt{ID: id, Version: row.Version, State: row.State}, nil
}
