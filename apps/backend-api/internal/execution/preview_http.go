package execution

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

type PreviewSessionInput struct {
	SessionID string `path:"session_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type previewCreateInput struct {
	ExecutionInput
	Body struct {
		Kind string `json:"kind" enum:"SERVICE" default:"SERVICE"`
		Port int    `json:"port" minimum:"1" maximum:"65535"`
	}
}
type previewAccessInput struct {
	PreviewSessionInput
	Body struct {
		ReturnPath string `json:"return_path,omitempty" maxLength:"2000"`
	}
}
type previewLabelAccessInput struct {
	Body struct {
		Label      string `json:"label" pattern:"^[a-z2-7]{26}$"`
		ReturnPath string `json:"return_path,omitempty" maxLength:"2000"`
	}
}
type previewCreateOutput struct {
	Status int
	Body   respx.Envelope[PreviewSessionView]
}
type previewGrantBody struct {
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[PreviewAccessGrant]
}

func previewHTTPError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, fleet.ErrTunnelQuota):
		return respx.ErrStatus(ctx, http.StatusTooManyRequests, 140486, fleeti18n.TunnelQuota, nil)
	case errors.Is(err, ErrPreviewPortNotOwned):
		return respx.ErrStatus(ctx, 403, 130403, "execution.preview_port_not_owned", map[string]any{"reason_code": "PORT_NOT_OWNED"})
	case errors.Is(err, ErrPreviewAccessDisabled):
		return respx.ErrStatus(ctx, 403, 130403, "execution.preview_access_disabled", map[string]any{"reason_code": "MACHINE_ACCESS_DISABLED"})
	case errors.Is(err, ErrPreviewUnavailable):
		return respx.ErrStatus(ctx, 503, 140492, "execution.preview_unavailable", nil)
	case errors.Is(err, ErrPreviewQuota):
		return respx.ErrStatus(ctx, 429, 130463, "execution.preview_quota", map[string]any{"limit": 3})
	default:
		return httpError(ctx, err)
	}
}
func (m *Module) previewOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindPreviewSessions(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID, EntityID: row.ProjectID}, true, nil
}
func (m *Module) registerPreviewHTTP(api huma.API) error {
	op := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{"previews"}, Errors: []int{401, 403, 404, 409, 422, 429, 503}}
	}
	authz.RegisterOwnedTenantMember(m.registrar, api, op("execution.preview-ports.list", http.MethodGet, "/executions/{execution_id}/offered-ports"), "execution_id", m.owner, m.offeredPreviewPortsHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("execution.previews.list", http.MethodGet, "/executions/{execution_id}/preview-sessions"), "execution_id", m.owner, m.listPreviewsHTTP)
	create := op("execution.previews.ensure", http.MethodPost, "/executions/{execution_id}/preview-sessions")
	create.DefaultStatus = 201
	authz.RegisterOwnedTenantMember(m.registrar, api, create, "execution_id", m.owner, m.createPreviewHTTP)
	// Both outcomes carry the same domain envelope; the status communicates reuse.
	registered := api.OpenAPI().Paths[create.Path].Post
	if response := registered.Responses["201"]; response != nil {
		existing := *response
		existing.Description = "Existing active preview session"
		registered.Responses["200"] = &existing
	}

	authz.RegisterOwnedTenantMember(m.registrar, api, op("preview.read", http.MethodGet, "/preview-sessions/{session_id}"), "session_id", m.previewOwner, m.getPreviewHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("preview.terminate", http.MethodPut, "/preview-sessions/{session_id}/termination"), "session_id", m.previewOwner, m.stopPreviewHTTP)
	grant := op("preview.access-grants.create", http.MethodPost, "/preview-sessions/{session_id}/access-grants")
	grant.DefaultStatus = http.StatusCreated
	authz.RegisterSessionAuthenticated(m.registrar, api, grant, m.issuePreviewAccessHTTP)
	labelGrant := op("preview.label-access.create", http.MethodPost, "/preview-access-grants")
	labelGrant.DefaultStatus = http.StatusCreated
	authz.RegisterSessionAuthenticated(m.registrar, api, labelGrant, m.issuePreviewLabelAccessHTTP)
	return nil
}
func (m *Module) listPreviewsHTTP(ctx context.Context, in *ExecutionInput) (*respx.Body[[]PreviewSessionView], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.ListPreviews(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) createPreviewHTTP(ctx context.Context, in *previewCreateInput) (*previewCreateOutput, error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.CreatePreview(ctx, authn.TenantIDFromContext(ctx), id, in.Body.Port)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	status := 201
	if out.Reused {
		status = 200
	}
	return &previewCreateOutput{Status: status, Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) getPreviewHTTP(ctx context.Context, in *PreviewSessionInput) (*respx.Body[PreviewSessionView], error) {
	id, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.GetPreview(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) stopPreviewHTTP(ctx context.Context, in *PreviewSessionInput) (*respx.Body[PreviewSessionView], error) {
	id, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.StopPreview(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

// previewBrowserContext binds only a server-resolved preview tenant. It preserves
// verified identity claims; project permission is always checked by the domain.
func previewBrowserContext(ctx context.Context, tenant guid.ID) (context.Context, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims.PrincipalID <= 0 || claims.SubjectType != authn.SubjectTypePrincipal {
		return ctx, ErrForbidden
	}
	bound := *claims
	bound.TenantID = tenant
	return authn.WithClaims(ctx, &bound), nil
}
func (m *Module) issuePreviewAccessHTTP(ctx context.Context, in *previewAccessInput) (*previewGrantBody, error) {
	id, err := guid.RequireID(in.SessionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	row, err := m.previewOn(ctx, db, id, false)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	ctx, err = previewBrowserContext(ctx, row.TenantID)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	out, err := m.IssuePreviewAccess(ctx, row.TenantID, id, in.Body.ReturnPath)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return &previewGrantBody{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) issuePreviewLabelAccessHTTP(ctx context.Context, in *previewLabelAccessInput) (*previewGrantBody, error) {
	db, err := m.writer()
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	q := query.BindPreviewSessions(db, m.names)
	row, err := q.WithContext(ctx).Where(q.Label.Eq(in.Body.Label)).First()
	if err != nil {
		return nil, previewHTTPError(ctx, ErrNotFound)
	}
	ctx, err = previewBrowserContext(ctx, row.TenantID)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	out, err := m.IssuePreviewAccess(ctx, row.TenantID, row.ID, in.Body.ReturnPath)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return &previewGrantBody{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}

func (m *Module) offeredPreviewPortsHTTP(ctx context.Context, in *ExecutionInput) (*respx.Body[[]PreviewOfferedPort], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, previewHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.OfferedPreviewPorts(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, previewHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
