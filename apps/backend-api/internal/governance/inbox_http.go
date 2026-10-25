package governance

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

type inboxItemInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"inbox_item_id" maxLength:"100" doc:"Kind and immutable source ID separated by a colon."`
}
type inboxAckInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"inbox_item_id" maxLength:"100" doc:"Kind and immutable source ID separated by a colon."`
	Body     struct {
		Revision string `json:"revision" pattern:"^[a-f0-9]{64}$"`
	}
}
type inboxCountsInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Kind      string `query:"kind" maxLength:"64"`
}

func (m *Module) registerInboxCommandsHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "governance.inbox.counts", Method: http.MethodGet, Path: "/inbox-counts", Summary: "Count my current authorized decision sources", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, m.inboxCountsHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "governance.inbox.get", Method: http.MethodGet, Path: "/inbox-items/{inbox_item_id}", Summary: "Read one of my current decision sources", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, m.inboxItemHTTP)
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "governance.inbox.acknowledge", Method: http.MethodPost, Path: "/inbox-items/{inbox_item_id}/acknowledgements", Summary: "Acknowledge my current clarification or escalation notice", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, m.inboxAckHTTP)
}
func parseInboxIdentity(in *inboxItemInput) (guid.ID, string, guid.ID, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return 0, "", 0, ErrInvalidArgument
	}
	kind, source, ok := strings.Cut(in.ID, ":")
	if !ok {
		return 0, "", 0, ErrInvalidArgument
	}
	id, err := guid.RequireID(source)
	if err != nil {
		return 0, "", 0, ErrInvalidArgument
	}
	return tenant, kind, id, nil
}
func (m *Module) inboxItemHTTP(ctx context.Context, in *inboxItemInput) (*respx.Body[InboxItem], error) {
	tenant, kind, id, err := parseInboxIdentity(in)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	item, err := m.inboxItem(ctx, tenant, kind, id, false)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	item.SourceRevision = inboxRevision(item)
	return respx.OK(ctx, item), nil
}
func (m *Module) inboxCountsHTTP(ctx context.Context, in *inboxCountsInput) (*respx.Body[InboxCounts], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil || tenant != authn.TenantIDFromContext(ctx) {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, err = guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
	}
	out, err := m.inboxCounts(ctx, InboxRequest{TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), ProjectID: project, Kind: in.Kind})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) inboxAckHTTP(ctx context.Context, in *inboxAckInput) (*respx.Body[InboxItem], error) {
	tenant, kind, id, err := parseInboxIdentity(&inboxItemInput{TenantID: in.TenantID, ID: in.ID})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	var out InboxItem
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.acknowledgeInboxOn(ctx, tx, tenant, kind, id, in.Body.Revision)
		return e
	})
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	out.SourceRevision = inboxRevision(out)
	return respx.OK(ctx, out), nil
}
