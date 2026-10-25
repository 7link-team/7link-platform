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

const andonPath = workPath + "/{work_item_id}/andon"

type andonInput struct {
	EntityID   string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	WorkItemID string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key        string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body       andonBody
}

func (m *Module) registerAndonHTTP(api huma.API) {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.andon-pull", Method: http.MethodPost, Path: andonPath, Summary: "Pull a durable Andon hold without claiming runner pause", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "andon", Verb: "pull"}, m.pullAndonHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.andon-clear", Method: http.MethodPost, Path: andonPath + "/clear", Summary: "Clear an Andon hold with a Human disposition", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "andon", Verb: "clear"}, m.clearAndonHTTP)
}
func (m *Module) pullAndonHTTP(ctx context.Context, in *andonInput) (*deliveryCommandOutput[andonView], error) {
	return m.andonCommandHTTP(ctx, in, false)
}
func (m *Module) clearAndonHTTP(ctx context.Context, in *andonInput) (*deliveryCommandOutput[andonView], error) {
	return m.andonCommandHTTP(ctx, in, true)
}
func (m *Module) andonCommandHTTP(ctx context.Context, in *andonInput, release bool) (*deliveryCommandOutput[andonView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.WorkItemID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, resultHTTPError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	owner, e := m.preparationOwner(ctx, m.runtime.Database().Write(), tenant, project, id)
	if e != nil {
		return nil, resultHTTPError(ctx, e)
	}
	operation, path := "work_item.andon-pull", andonPath
	if release {
		operation, path = "work_item.andon-clear", andonPath+"/clear"
	}
	return deliveryCommand(ctx, m, "work_item", resultHTTPError, operation, http.MethodPost, path, in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "work_item_id": id.String()}, func(tx *gorm.DB) (andonView, guid.ID, error) {
		v, e := m.changeAndonOn(ctx, tx, tenant, project, id, actor, owner, in.Body, release)
		return v, id, e
	}, func(tx *gorm.DB) error {
		if e := m.lockHoldPrincipalsOn(ctx, tx, tenant, project, id, actor, owner); e != nil {
			return e
		}
		_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, "work_item_read", !release)
		if e != nil {
			return e
		}
		_, e = m.andonAuthorityOn(ctx, tx, work, actor, release)
		return e
	})
}
