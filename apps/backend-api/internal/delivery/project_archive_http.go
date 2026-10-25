package delivery

import (
	deliveryi18n "7link.com/platform/internal/delivery/i18n"
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

const projectArchivePath = "/entities/{entity_id}/project/archive"

type projectArchiveInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted project concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           projectArchiveBody
}
type projectRestoreInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IfMatch        string `header:"If-Match" required:"true" doc:"Quoted project concurrency version."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           struct {
		Reason string `json:"reason" minLength:"10" maxLength:"2000"`
	}
}

func archiveHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrArchivePreflight) {
		return respx.ErrStatus(ctx, 409, 127090, deliveryi18n.Namespace+".archive_preflight", nil)
	}
	return workHTTPError(ctx, err)
}
func (m *Module) registerProjectArchiveHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.entityOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.archive-preflight", Method: http.MethodGet, Path: projectArchivePath + "-preflight", Summary: "Inspect current project archive obligations and owner receipts", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.projectArchivePreflightHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.archive", Method: http.MethodPost, Path: projectArchivePath, Summary: "Archive a project after all owner dispositions are complete", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.archiveProjectHTTP)
	path := "/entities/{entity_id}/project/restore"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "project.restore", Method: http.MethodPost, Path: path, Summary: "Explicitly restore project access without restarting resources", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, owner, authz.Guard{Resource: "project", Verb: "manage"}, m.restoreProjectHTTP)
	if err := authz.RegisterRequestContract(api, projectArchivePath); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, path)
}
func (m *Module) projectArchivePreflightHTTP(ctx context.Context, in *stageReadinessInput) (*respx.Body[projectArchivePreflight], error) {
	project, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, archiveHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, archiveHTTPError(ctx, ErrUnavailable)
	}
	var out projectArchivePreflight
	err = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, e := m.archiveProjectOn(ctx, tx, authn.TenantIDFromContext(ctx), project, authn.PrincipalIDFromContext(ctx))
		if e != nil {
			return e
		}
		out, e = m.archivePreflightOn(ctx, tx, p)
		return e
	})
	if err != nil {
		return nil, archiveHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) archiveProjectHTTP(ctx context.Context, in *projectArchiveInput) (*deliveryCommandOutput[ProjectView], error) {
	return m.projectArchiveCommandHTTP(ctx, in.EntityID, in.IfMatch, in.IdempotencyKey, false, in.Body)
}
func (m *Module) restoreProjectHTTP(ctx context.Context, in *projectRestoreInput) (*deliveryCommandOutput[ProjectView], error) {
	return m.projectArchiveCommandHTTP(ctx, in.EntityID, in.IfMatch, in.IdempotencyKey, true, projectArchiveBody{Reason: in.Body.Reason})
}
func (m *Module) projectArchiveCommandHTTP(ctx context.Context, rawID, etag, key string, restore bool, body projectArchiveBody) (*deliveryCommandOutput[ProjectView], error) {
	project, _, version, err := deliveryEditIDs(rawID, rawID, etag)
	if err != nil || key == "" {
		return nil, archiveHTTPError(ctx, ErrInvalidArgument)
	}
	action, path := "archive", projectArchivePath
	if restore {
		action, path = "restore", "/entities/{entity_id}/project/restore"
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	request := struct {
		Version int                `json:"version"`
		Body    projectArchiveBody `json:"body"`
	}{version, body}
	return deliveryCommand(ctx, m, "project", archiveHTTPError, "project."+action, http.MethodPost, path, key, 200, request, map[string]string{"entity_id": project.String()}, func(tx *gorm.DB) (ProjectView, guid.ID, error) {
		var v ProjectView
		e := tx.Transaction(func(inner *gorm.DB) error {
			var err error
			v, err = m.changeProjectArchiveOn(ctx, inner, tenant, project, actor, version, restore, body)
			return err
		})
		return v, project, e
	}, func(tx *gorm.DB) error {
		_, e := m.archiveProjectOn(ctx, tx, tenant, project, actor)
		return e
	})
}
