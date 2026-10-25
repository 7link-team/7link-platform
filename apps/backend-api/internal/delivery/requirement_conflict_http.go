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

const requirementConflictPath = workPath + "/{work_item_id}/requirement-conflicts"

type requirementConflictInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key        string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body       requirementConflictBody
}

func (m *Module) registerRequirementConflictHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.requirement-conflict", Method: http.MethodPost, Path: requirementConflictPath, Summary: "Raise or resolve a source requirement conflict", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "work_item", Verb: "write"}, m.requirementConflictHTTP)
}
func (m *Module) requirementConflictHTTP(ctx context.Context, in *requirementConflictInput) (*deliveryCommandOutput[WorkView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.WorkItemID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, err := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if err != nil {
		return nil, resultHTTPError(ctx, err)
	}
	authorize := func(tx *gorm.DB) error {
		permission := "work_item_write"
		if in.Body.Action == "RESOLVE" {
			permission = "scope_change"
		}
		_, _, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, permission, in.Body.Action == "RAISE")
		return err
	}
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, "work_item.requirement-conflict", http.MethodPost, requirementConflictPath, in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (WorkView, guid.ID, error) {
		out, err := m.requirementConflictOn(ctx, tx, tenant, project, id, actor, owner, in.Body)
		return out, id, err
	}, authorize)
}
