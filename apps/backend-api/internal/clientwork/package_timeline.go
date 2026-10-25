package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"time"
)

type PackageTimelineView struct {
	ID          guid.ID   `json:"id"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	Version     int       `json:"version"`
	ContentHash string    `json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
}
type PackageTimelineInput struct {
	ListInput
	ID string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// Called by the package command's transactional audit/outbox boundary. This
// records observed transitions only; old packages receive no invented history.
func (m *Module) recordPackageTimelineOn(ctx context.Context, tx *gorm.DB, p, id guid.ID, kind string, version int) error {
	row, e := m.packageOn(ctx, tx, p, id, false)
	if e != nil {
		return e
	}
	if row.Version != version {
		return ErrConflict
	}
	entry, e := m.nextID()
	if e != nil {
		return e
	}
	return query.BindPackageTimeline(tx, m.names).WithContext(ctx).Create(&model.PackageTimeline{TenantID: row.TenantID, ID: entry, ProjectID: p, PackageID: id, Kind: kind, State: row.State, Version: version, ContentHash: row.ContentHash, CreatedAt: now()})
}
func (m *Module) registerPackageTimelineHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.package.timeline", Method: "GET", Path: entityBase + "/delivery-packages/{id}/timeline", Summary: "Read authorized immutable package transitions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "channel", Verb: "read"}, m.packageTimelineHTTP)
}
func (m *Module) packageTimelineHTTP(ctx context.Context, in *PackageTimelineInput) (*respx.Body[Page[PackageTimelineView]], error) {
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return readDomain(ctx, m, &in.ListInput, "package-timeline:"+id.String(), "delivery_read", true, func(tx *gorm.DB, external bool, last guid.ID, n int) ([]PackageTimelineView, []guid.ID, error) {
		p, e := guid.RequireID(in.EntityID)
		if e != nil {
			return nil, nil, ErrInvalid
		}
		row, e := m.packageOn(ctx, tx, p, id, external)
		if e != nil {
			return nil, nil, e
		}
		q := query.BindPackageTimeline(tx, m.names)
		base := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(p), q.PackageID.Eq(id), q.ID.Gt(last))
		if external {
			base = base.Where(q.State.Neq("DRAFT"))
		}
		rows, e := base.Order(q.ID).Limit(n).Find()
		if e != nil {
			return nil, nil, e
		}
		out := make([]PackageTimelineView, 0, len(rows))
		ids := make([]guid.ID, 0, len(rows))
		for _, r := range rows {
			out = append(out, PackageTimelineView{ID: r.ID, Kind: r.Kind, State: r.State, Version: r.Version, ContentHash: r.ContentHash, CreatedAt: r.CreatedAt})
			ids = append(ids, r.ID)
		}
		return out, ids, nil
	})
}
