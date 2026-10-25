package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const resultFeedbackPath = workPath + "/{work_item_id}/results/{result_id}/feedback"

func commentFeedbackHTTPError(ctx context.Context, err error) error {
	var conversion *commentDefectError
	if errors.As(err, &conversion) {
		data := map[string]any{"reason": conversion.Reason}
		if conversion.TargetID > 0 {
			data["target_id"] = conversion.TargetID.String()
		}
		return respx.ErrStatus(ctx, 422, 120471, "delivery.comment_conversion", data)
	}
	return workHTTPError(ctx, err)
}

type resultFeedbackInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ResultID       string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           resultFeedbackBody
}
type resultFeedbackListInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ResultID   string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset     int    `query:"offset" default:"0" minimum:"0"`
	Limit      int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

func (m *Module) registerFeedbackHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.feedback-create", Method: http.MethodPost, Path: resultFeedbackPath, Summary: "Comment on an exact Human result version", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "artifact_comment", Verb: "write"}, m.appendResultFeedbackHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.feedback-list", Method: http.MethodGet, Path: resultFeedbackPath, Summary: "List comments for an exact result version", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.listResultFeedbackHTTP)
	return authz.RegisterRequestContract(api, resultFeedbackPath)
}
func (m *Module) appendResultFeedbackHTTP(ctx context.Context, in *resultFeedbackInput) (*deliveryCommandOutput[resultFeedbackView], error) {
	project, e := guid.RequireID(in.EntityID)
	workID, e2 := guid.RequireID(in.WorkItemID)
	resultID, e3 := guid.RequireID(in.ResultID)
	if e != nil || e2 != nil || e3 != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, workID)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "result_feedback", commentFeedbackHTTPError, "result.feedback-create", http.MethodPost, resultFeedbackPath, in.IdempotencyKey, 201, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": workID.String(), "result_id": resultID.String()}, func(tx *gorm.DB) (resultFeedbackView, guid.ID, error) {
		v, e := m.appendResultFeedbackOn(ctx, tx, tenant, project, workID, resultID, actor, owner, in.Body)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, workID, actor, owner, "artifact_comment_write")
		return e
	})
}
func (m *Module) listResultFeedbackHTTP(ctx context.Context, in *resultFeedbackListInput) (*respx.Body[[]resultFeedbackView], error) {
	project, e := guid.RequireID(in.EntityID)
	workID, e2 := guid.RequireID(in.WorkItemID)
	resultID, e3 := guid.RequireID(in.ResultID)
	if e != nil || e2 != nil || e3 != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	var items []resultFeedbackView
	var total int64
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		items, total, e = m.resultFeedbackOn(ctx, tx, authn.TenantIDFromContext(ctx), project, workID, resultID, authn.PrincipalIDFromContext(ctx), in.Offset, in.Limit)
		return e
	})
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
}
