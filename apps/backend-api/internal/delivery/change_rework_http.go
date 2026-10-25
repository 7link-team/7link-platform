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

const workResumePath = workPath + "/{work_item_id}/resume"
const scopeReworkPath = workResultPath + "/request-changes"

type scopeReworkInput struct {
	ResultVersionInput
	Body scopeReworkBody
}
type workResumeInput struct {
	ResultMutationInput
	Body workResumeBody
}

func (m *Module) registerReworkHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.workOwner)
	errors := []int{400, 401, 403, 404, 409, 422, 500, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.request-changes", Method: http.MethodPost, Path: scopeReworkPath, Summary: "Request scope rework of a stale current Human result", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.scopeReworkHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.resume", Method: http.MethodPost, Path: workResumePath, Summary: "Authorize Human manual rework after an autonomy-limit pause", Tags: []string{"work-items"}, Errors: errors}, "work_item_id", owner, authz.Guard{Resource: "work_item", Verb: "read"}, m.resumeManualWorkHTTP)
	for _, path := range []string{scopeReworkPath, workResumePath} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) scopeReworkHTTP(ctx context.Context, in *scopeReworkInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.request-changes", "/request-changes", "work_item_read", "decision_request_changes", in.Body, func(tx *gorm.DB, tenant, project, work, actor, owner guid.ID, version int) (ResultView, error) {
		id, err := guid.RequireID(in.ResultID)
		if err != nil {
			return ResultView{}, ErrInvalidArgument
		}
		return m.scopeReworkOn(ctx, tx, tenant, project, work, id, actor, owner, version, in.Body)
	})
}

func (m *Module) resumeManualWorkHTTP(ctx context.Context, in *workResumeInput) (*deliveryCommandOutput[workResumeView], error) {
	project, id, version, err := deliveryEditIDs(in.EntityID, in.WorkItemID, in.IfMatch)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, "work_item.resume", http.MethodPost, workResumePath, in.IdempotencyKey, 200, struct {
		Version int            `json:"version"`
		Body    workResumeBody `json:"body"`
	}{version, in.Body}, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (workResumeView, guid.ID, error) {
		view, e := m.resumeManualWorkOn(ctx, tx, tenant, project, id, actor, owner, version, in.Body)
		return view, id, e
	}, func(tx *gorm.DB) error {
		_, work, _, e := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_read")
		if e != nil {
			return e
		}
		_, e = m.manualResumeAuthorityOn(ctx, tx, work, actor)
		return e
	})
}
