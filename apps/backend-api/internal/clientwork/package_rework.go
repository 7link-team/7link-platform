package clientwork

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func (m *Module) packageReworkOn(ctx context.Context, tx *gorm.DB, row *model.DeliveryPackage, receipt, recipient guid.ID, outcomes []PackageItemDecision) (intake.ReworkSourceView, bool, error) {
	var out intake.ReworkSourceView
	if m.packageSources.ReadOn == nil || m.reworkSources.CreateOn == nil {
		return out, false, nil
	}
	var manifest packageManifest
	if e := json.Unmarshal([]byte(row.Manifest), &manifest); e != nil {
		return out, false, e
	}
	items := packageItems(row, manifest.Content)
	if len(items) != len(manifest.Refs) || len(items) != len(outcomes) {
		return out, false, ErrConflict
	}
	var refs []delivery.ClientArtifactRef
	var rejected []PackageItemDecision
	for i, item := range items {
		if outcomes[i].ItemID != item.ItemID {
			return out, false, ErrConflict
		}
		if outcomes[i].Result == "REJECTED" {
			refs = append(refs, manifest.Refs[i])
			rejected = append(rejected, outcomes[i])
		}
	}
	if len(refs) == 0 {
		return out, false, ErrConflict
	}
	facts, e := m.packageSources.ReadOn(ctx, tx, row.TenantID, row.ProjectID, refs)
	if e != nil {
		return out, false, e
	}
	if len(facts) != len(refs) {
		return out, false, ErrUnavailable
	}
	draft := intake.ReworkDraft{PackageID: row.ID, ReceiptID: receipt, RecipientID: recipient, ContentHash: row.ContentHash, Items: make([]intake.ReworkItem, 0, len(refs))}
	for i, ref := range refs {
		if facts[i].WorkItemID != ref.WorkItemID {
			return out, false, ErrConflict
		}
		anchors := make([]intake.ReworkAnchor, 0, len(rejected[i].Anchors))
		for _, a := range rejected[i].Anchors {
			raw, err := json.Marshal(a.ArtifactRef)
			if err != nil {
				return out, false, err
			}
			anchors = append(anchors, intake.ReworkAnchor{Anchor: a.Anchor, ThreadID: a.ThreadID, AnchorKind: a.AnchorKind, AnchorExcerpt: a.AnchorExcerpt, VersionRef: a.VersionRef, ArtifactRef: raw})
		}
		draft.Items = append(draft.Items, intake.ReworkItem{Anchors: anchors, ItemID: rejected[i].ItemID, WorkItemID: ref.WorkItemID, ResultID: ref.ResultID, EvidenceID: ref.EvidenceID, RequirementID: facts[i].RequirementID, SourceIntakeID: facts[i].IntakeID, FeedbackCategory: rejected[i].FeedbackCategory, Comment: rejected[i].Comment})
	}
	return m.reworkSources.CreateOn(ctx, tx, row.TenantID, row.ProjectID, draft)
}

func (m *Module) retryPackageReworkOn(ctx context.Context, tx *gorm.DB, project, id guid.ID, version int) (Receipt, error) {
	if _, e := m.authorize(ctx, tx, project, "scope_change", false); e != nil {
		return Receipt{}, e
	}
	row, e := m.packageOn(ctx, tx, project, id, false)
	if e != nil {
		return Receipt{}, e
	}
	if row.Version != version || (row.State != "REJECTED" && row.State != "PARTIALLY_ACCEPTED") {
		return Receipt{}, ErrConflict
	}
	q := query.BindPackageReceipt(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(project), q.PackageID.Eq(id)).Take()
	if e != nil {
		return Receipt{}, e
	}
	if m.reworkSources.ReadOn == nil {
		return Receipt{}, ErrUnavailable
	}
	prior, found, e := m.reworkSources.ReadOn(ctx, tx, row.TenantID, project, r.ID)
	if e != nil {
		return Receipt{}, e
	}
	if found {
		return Receipt{ID: prior.ID, Version: prior.Version, State: prior.State}, nil
	}
	var outcomes []PackageItemDecision
	if r.ItemOutcomes != nil {
		if e = json.Unmarshal([]byte(*r.ItemOutcomes), &outcomes); e != nil {
			return Receipt{}, e
		}
	} else {
		return Receipt{}, ErrUnavailable
	}
	source, available, e := m.packageReworkOn(ctx, tx, row, r.ID, r.ActorID, outcomes)
	if e != nil {
		return Receipt{}, e
	}
	if !available {
		return Receipt{}, ErrUnavailable
	}
	return Receipt{ID: source.ID, Version: source.Version, State: source.State}, nil
}

func (m *Module) registerPackageReworkHTTP(api huma.API) {
	path := entityBase + "/delivery-packages/{id}/rework"
	guard := authz.Guard{Resource: "scope", Verb: "change"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_rework.create", Method: "POST", Path: path, DefaultStatus: 201, Summary: "Create a missing rework Intake from the immutable rejected package receipt", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), guard, m.retryPackageReworkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package_rework.get", Method: "GET", Path: path, Summary: "Read the authorized immutable rejected-package Intake source", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), guard, m.getPackageReworkHTTP)
}
func (m *Module) retryPackageReworkHTTP(ctx context.Context, in *EditInput[ClientworkPublishBody]) (*CommandOutput, error) {
	project, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return m.command(ctx, project, "clientwork.package_rework.create", "POST", entityBase+"/delivery-packages/"+id.String()+"/rework", in.Key, 201, in, "scope_change", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.retryPackageReworkOn(ctx, tx, project, id, v) })
}
func (m *Module) getPackageReworkHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[intake.ReworkSourceView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil || m.reworkSources.ReadOn == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out intake.ReworkSourceView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.authorize(ctx, tx, project, "scope_change", false); e != nil {
			return e
		}
		row, e := m.packageOn(ctx, tx, project, id, false)
		if e != nil {
			return e
		}
		q := query.BindPackageReceipt(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.PackageID.Eq(row.ID)).Take()
		if e != nil {
			return e
		}
		var found bool
		out, found, e = m.reworkSources.ReadOn(ctx, tx, row.TenantID, project, r.ID)
		if e == nil && !found {
			return ErrNotFound
		}
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
