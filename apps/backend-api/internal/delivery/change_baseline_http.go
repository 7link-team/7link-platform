package delivery

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const workBaselinePath = workPath + "/{work_item_id}/baseline"

type workBaselineInput struct {
	ResultMutationInput
	Body workBaselineBody
}

func (m *Module) registerBaselineHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.baseline", Method: http.MethodPut, Path: workBaselinePath, Summary: "Confirm current requirement criteria in a new work contract", Tags: []string{"work-items"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "work_item_id", authz.ResourceOwnerResolver(m.workOwner), authz.Guard{Resource: "work_item", Verb: "write"}, m.workBaselineHTTP)
	return authz.RegisterRequestContract(api, workBaselinePath)
}

func (m *Module) workBaselineHTTP(ctx context.Context, in *workBaselineInput) (*deliveryCommandOutput[workBaselineView], error) {
	project, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, workHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", workHTTPError, "work_item.baseline", http.MethodPut, workBaselinePath, in.IdempotencyKey, 200, struct {
		Version int              `json:"version"`
		Body    workBaselineBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (workBaselineView, guid.ID, error) {
		view, e := m.workBaselineOn(ctx, tx, tenant, project, id, actor, owner, version, in.Body)
		return view, id, e
	}, func(tx *gorm.DB) error {
		_, _, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
		return e
	})
}
