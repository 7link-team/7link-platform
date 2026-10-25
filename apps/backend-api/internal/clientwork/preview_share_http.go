package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func (m *Module) registerShareHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.owner)
	errs := []int{401, 403, 404, 409, 410, 422, 500, 503}
	path := entityBase + "/preview-shares"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview_share.create", Method: "POST", Path: path, Summary: "Request an expiring fixed preview for an invited project member", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.requestShareHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview_share.list", Method: "GET", Path: path, Summary: "List authorized fixed preview shares and their effective expiry states", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.listSharesHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview_share.get", Method: "GET", Path: path + "/{id}", Summary: "Read a currently authorized unexpired preview share", Tags: []string{"entities"}, Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.getShareHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview_share.decide", Method: "POST", Path: path + "/{id}/decisions", Summary: "Approve, reject or revoke a preview share with current authority", Tags: []string{"entities"}, DefaultStatus: 201, Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.decideShareHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.preview_share.content", Method: "GET", Path: path + "/{id}/content", Summary: "View the fixed shared preview with current membership and expiry checks", Tags: []string{"entities"}, Responses: binaryContentResponses(), Errors: errs}, authz.EntityPath, owner, authz.Guard{Resource: "channel", Verb: "read"}, m.shareContentHTTP)
	if !m.registrar.IsDeclarationOnly() {
		if e := authz.RegisterRequestContract(api, path); e != nil {
			return e
		}
		return authz.RegisterRequestContract(api, path+"/{id}/decisions")
	}
	return nil
}
func (m *Module) requestShareHTTP(ctx context.Context, in *CreateInput[PreviewShareBody]) (*CommandOutput, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.preview_share.create", "POST", entityBase+"/preview-shares", in.Key, 201, in.Body, "project_manage", false, nil, func(tx *gorm.DB) (Receipt, error) { return m.requestShareOn(ctx, tx, p, in.Body) })
}
func (m *Module) decideShareHTTP(ctx context.Context, in *EditInput[PreviewShareDecision]) (*CommandOutput, error) {
	p, id, v, e := editIDs(in.EntityID, in.ID, in.IfMatch)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	permission := "preview_share_approve"
	if in.Body.Decision == "REVOKE" {
		permission = "project_manage"
	}
	return m.command(ctx, p, "clientwork.preview_share.decide", "POST", entityBase+"/preview-shares/"+id.String()+"/decisions", in.Key, 201, in, permission, false, nil, func(tx *gorm.DB) (Receipt, error) { return m.decideShareOn(ctx, tx, p, id, v, in.Body) })
}
func (m *Module) listSharesHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[PreviewShareView]], error) {
	return readDomain(ctx, m, in, "preview-shares", "project_read", true, func(tx *gorm.DB, external bool, last guid.ID, n int) ([]PreviewShareView, []guid.ID, error) {
		q := query.BindPreviewShare(tx, m.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(authn.EntityIDFromContext(ctx)), q.ID.Gt(last))
		if external {
			base = base.Where(q.RecipientID.Eq(authn.PrincipalIDFromContext(ctx)), q.State.Neq("PENDING_APPROVAL"))
		}
		rows, e := base.Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]PreviewShareView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, row := range rows {
			out = append(out, shareRecord(row))
			ids = append(ids, row.ID)
		}
		return out, ids, nil
	})
}
func (m *Module) getShareHTTP(ctx context.Context, in *ObjectInput) (*respx.Body[PreviewShareView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var out PreviewShareView
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		external, e := m.authorize(ctx, tx, p, "project_read", true)
		if e != nil {
			return e
		}
		row, e := m.shareOn(ctx, tx, p, id)
		if e != nil {
			return e
		}
		if !external && shareRecord(row).State != "ACTIVE" {
			out = shareRecord(row)
			return nil
		}
		row, e = m.authorizedShareOn(ctx, tx, p, id)
		if e != nil {
			return e
		}
		preview, e := m.previewOn(ctx, tx, p, row.PreviewID, false)
		if e != nil {
			return e
		}
		view := previewView(preview)
		out = shareRecord(row)
		out.Preview = &view
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) shareContentHTTP(ctx context.Context, in *ObjectInput) (*huma.StreamResponse, error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.streamPinnedContent(ctx, p, true, func(tx *gorm.DB, _ bool) (guid.ID, string, error) {
		row, e := m.authorizedShareOn(ctx, tx, p, id)
		if e != nil {
			return 0, "", e
		}
		if row.BindingID == nil {
			return 0, "", ErrNotFound
		}
		preview, e := m.previewOn(ctx, tx, p, row.PreviewID, false)
		if e != nil {
			return 0, "", e
		}
		return *row.BindingID, preview.ContentHash, nil
	})
}
