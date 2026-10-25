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

const projectAndonPath = "/entities/{entity_id}/project/andon"

type projectAndonInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     projectAndonBody
}

func (m *Module) registerProjectAndonHTTP(api huma.API) {
	for _, release := range []bool{false, true} {
		path, operation, verb := projectAndonPath, "project.andon-pull", "pull"
		if release {
			path += "/clear"
			operation, verb = "project.andon-clear", "clear"
		}
		authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: operation, Method: http.MethodPost, Path: path, Summary: "Change a project Andon hold", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "andon", Verb: verb}, func(ctx context.Context, in *projectAndonInput) (*deliveryCommandOutput[projectAndonView], error) {
			return m.projectAndonHTTP(ctx, in, release)
		})
	}
}
func (m *Module) projectAndonHTTP(ctx context.Context, in *projectAndonInput, release bool) (*deliveryCommandOutput[projectAndonView], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	path, operation := projectAndonPath, "project.andon-pull"
	if release {
		path += "/clear"
		operation = "project.andon-clear"
	}
	return deliveryCommand(ctx, m, "project", resultHTTPError, operation, http.MethodPost, path, in.Key, 200, in.Body, map[string]string{"entity_id": project.String()}, func(tx *gorm.DB) (projectAndonView, guid.ID, error) {
		v, e := m.projectAndonOn(ctx, tx, tenant, project, actor, in.Body, release)
		return v, project, e
	}, func(tx *gorm.DB) error {
		_, _, e := m.projectAndonAuthorityOn(ctx, tx, tenant, project, actor, release)
		return e
	})
}
