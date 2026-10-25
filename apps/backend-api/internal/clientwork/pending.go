package clientwork

import (
	"context"
	"slices"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// ClientPendingItem contains only the recipient's published decision target.
// The destination command independently checks its exact version and hash.
type ClientPendingItem struct {
	ID      guid.ID `json:"id"`
	Kind    string  `json:"kind" enum:"SIGNOFF,CHANGE,ACCEPTANCE"`
	Title   string  `json:"title"`
	Version int     `json:"version"`
}

func (m *Module) registerPendingHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.pending.list", Method: "GET", Path: entityBase + "/client-pending", Summary: "List the current client's published pending decisions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "channel", Verb: "read"}, m.listPendingHTTP)
}

func (m *Module) listPendingHTTP(ctx context.Context, in *ListInput) (*respx.Body[Page[ClientPendingItem]], error) {
	return readDomain(ctx, m, in, "client-pending", "channel_read", true, func(tx *gorm.DB, external bool, last guid.ID, limit int) ([]ClientPendingItem, []guid.ID, error) {
		if !external {
			return nil, nil, ErrForbidden
		}
		tenant, project, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
		out := make([]ClientPendingItem, 0, 3*limit)
		s, r := query.BindSheets(tx, m.names), query.BindRevisions(tx, m.names)
		sheets, e := s.WithContext(ctx).Join(r, r.TenantID.EqCol(s.TenantID), r.ProjectID.EqCol(s.ProjectID), r.SheetID.EqCol(s.ID), r.Revision.EqCol(s.PublishedRevision)).Where(s.TenantID.Eq(tenant), s.ProjectID.Eq(project), s.State.Eq("AWAITING_SIGNOFF"), s.CurrentRevision.EqCol(s.PublishedRevision), r.SignerID.Eq(actor), s.ID.Gt(last)).Order(s.ID).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		for _, row := range sheets {
			// Use the immutable published title, never an unpublished draft title.
			view, e := m.sheetView(ctx, tx, row, true, 0)
			if e != nil {
				return nil, nil, e
			}
			out = append(out, ClientPendingItem{row.ID, "SIGNOFF", view.Title, row.Version})
		}
		c := query.BindChangeRequest(tx, m.names)
		changes, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.ActorID.Eq(actor), c.State.Eq("AWAITING_CONFIRMATION"), c.ID.Gt(last)).Order(c.ID).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		for _, row := range changes {
			out = append(out, ClientPendingItem{row.ID, "CHANGE", row.Description, row.Version})
		}
		p := query.BindDeliveryPackage(tx, m.names)
		packages, e := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.RecipientID.Eq(actor), p.State.Eq("PUBLISHED"), p.ID.Gt(last)).Order(p.ID).Limit(limit).Find()
		if e != nil {
			return nil, nil, e
		}
		for _, row := range packages {
			out = append(out, ClientPendingItem{row.ID, "ACCEPTANCE", row.Title, row.Version})
		}
		// IDs share the canonical snowflake source. Fetch a full page from each
		// kind before merging, so neither type starvation nor early truncation
		// can hide the next globally ordered item.
		slices.SortFunc(out, func(a, b ClientPendingItem) int {
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
		if len(out) > limit {
			out = out[:limit]
		}
		ids := make([]guid.ID, 0, len(out))
		for _, item := range out {
			ids = append(ids, item.ID)
		}
		return out, ids, nil
	})
}
