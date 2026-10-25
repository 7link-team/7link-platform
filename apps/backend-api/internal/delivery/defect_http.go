package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type defectStageInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           defectStageBody
}

const defectStagePath = workPath + "/{work_item_id}/defect-stage"

func (m *Module) registerDefectHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.defect-stage", Method: http.MethodPost, Path: defectStagePath, Summary: "Record evidence-guarded manual defect verification", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "work_item", Verb: "write"}, m.defectStageHTTP)
	return authz.RegisterRequestContract(api, defectStagePath)
}
func (m *Module) defectStageHTTP(ctx context.Context, in *defectStageInput) (*deliveryCommandOutput[defectStageView], error) {
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
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, "work_item.defect-stage", http.MethodPost, defectStagePath, in.IdempotencyKey, 200, struct {
		Version int             `json:"version"`
		Body    defectStageBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (defectStageView, guid.ID, error) {
		v, e := m.defectStageOn(ctx, tx, tenant, project, id, actor, owner, version, in.Body)
		return v, id, e
	}, func(tx *gorm.DB) error {
		_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write", in.Body.Stage == "MERGED")
		if e != nil {
			return e
		}
		result, e := m.resultOn(ctx, tx, tenant, project, id, in.Body.ResultID)
		if e != nil {
			return e
		}
		if work.CurrentClaimID == nil || *work.CurrentClaimID != result.ID {
			return ErrResultState
		}
		if in.Body.Stage == "MERGED" || in.Body.Stage == "DEPLOYED" {
			return nil
		}
		code := "decision_approve"
		if in.Body.Stage == "REWORK" {
			code = "decision_request_changes"
		}
		_, e = m.resultAuthorityOn(ctx, tx, work, result, actor, code)
		return e
	})
}
