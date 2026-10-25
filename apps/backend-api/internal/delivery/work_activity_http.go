package delivery

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type workActivityListInput struct {
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	AfterSeq   int64  `query:"after_seq" minimum:"0" default:"0"`
	Limit      int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset     int    `query:"offset" minimum:"0" default:"0"`
}
type WorkActivityPage struct {
	CurrentSeq int64              `json:"current_seq"`
	Items      []WorkActivityView `json:"items"`
	HasMore    bool               `json:"has_more"`
}

func (m *Module) registerWorkActivityHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.activities", Method: "GET", Path: "/work-items/{work_item_id}/activities", Summary: "Read authorized ordered work item activities", Tags: []string{"work-items"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "work_item_id", authz.ResourceOwnerResolver(m.workOwner), authz.Guard{Resource: "work_item", Verb: "read"}, m.listWorkActivitiesHTTP)
}
func (m *Module) listWorkActivitiesHTTP(ctx context.Context, in *workActivityListInput) (*respx.Body[WorkActivityPage], error) {
	id, err := guid.RequireID(in.WorkItemID)
	if err != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m == nil || m.service == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, project, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	out := WorkActivityPage{Items: []WorkActivityView{}}
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Reuse the normal work projection's current IAM/data-scope check. The
		// activity reader itself is a trusted intra-transaction module capability.
		if _, e := m.readWork(ctx, tx, tenant, project, id, actor); e != nil {
			return e
		}
		var e error
		out.CurrentSeq, e = m.workActivityHeadOn(ctx, tx, tenant, id)
		if e != nil {
			return e
		}
		out.Items, e = m.workActivityPageOn(ctx, tx, tenant, id, in.AfterSeq, in.Limit, in.Offset)
		if e != nil {
			return e
		}
		if len(out.Items) > 0 {
			out.HasMore = out.Items[len(out.Items)-1].Seq < out.CurrentSeq
		}
		return nil
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
