package clientwork

import (
	ci "7link.com/platform/internal/clientwork/i18n"
	"7link.com/platform/internal/delivery"
	transportaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

type ClientAccessInput struct {
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type ClientAccess struct {
	External    bool                                `json:"external"`
	PrincipalID guid.ID                             `json:"principal_id"`
	Projects    Page[delivery.ClientProjectSummary] `json:"projects"`
}
type ProjectClientAccess struct {
	CanAccept   bool    `json:"can_accept"`
	External    bool    `json:"external"`
	PrincipalID guid.ID `json:"principal_id"`
}

func (m *Module) registerAccessHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "clientwork.access", Method: "GET", Path: "/client-access", Summary: "Read current client identity and authorized project projection", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}, m.accessHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.project_access", Method: "GET", Path: entityBase + "/client-access", Summary: "Read current project client authority", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "channel", Verb: "read"}, m.projectAccessHTTP)
}
func (m *Module) projectAccessHTTP(ctx context.Context, in *ListInput) (*respx.Body[ProjectClientAccess], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	if m.writer() == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	var ext bool
	var canAccept bool
	e = m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		ext, e = m.authorize(ctx, tx, p, "channel_read", true)
		if e != nil || !ext {
			return e
		}
		_, e = m.clientSignerOn(ctx, tx, p)
		if errors.Is(e, delivery.ErrWorkForbidden) || errors.Is(e, ErrForbidden) {
			return nil
		}
		canAccept = e == nil
		return e
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, ProjectClientAccess{CanAccept: canAccept, External: ext, PrincipalID: authn.PrincipalIDFromContext(ctx)}), nil
}
func (m *Module) accessHTTP(ctx context.Context, in *ClientAccessInput) (*respx.Body[ClientAccess], error) {
	if m.writer() == nil || m.clients == nil || m.members == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	out := ClientAccess{PrincipalID: actor, Projects: Page[delivery.ClientProjectSummary]{Items: []delivery.ClientProjectSummary{}}}
	limit := in.Limit
	if limit < 1 || limit > 100 {
		limit = 50
	}
	e := m.writer().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		active, e := m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
		if e != nil {
			return e
		}
		if !active {
			return ErrNotFound
		}
		out.External, e = m.clients.IsExternalOn(ctx, tx, tenant, actor)
		if e != nil || !out.External {
			return e
		}
		after, e := position(in.Cursor, cursorScope(ctx, 0, "client_projects"))
		if e != nil {
			return e
		}
		// Scope and project existence are checked before determining whether
		// another visible page exists. Hidden candidates never affect cursors.
		visibleIDs := make([]guid.ID, 0, limit+1)
		for len(out.Projects.Items) <= limit {
			ids, err := m.clients.ProjectsOn(ctx, tx, tenant, actor, after, 100)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				if id <= after {
					return ErrUnavailable
				}
				after = id
				ok, err := m.clients.IsClientOn(ctx, tx, tenant, id, actor)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				v, err := m.projects.ClientSummaryOn(ctx, tx, tenant, id)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				out.Projects.Items = append(out.Projects.Items, v)
				visibleIDs = append(visibleIDs, id)
				if len(out.Projects.Items) > limit {
					break
				}
			}
			if len(ids) < 100 {
				break
			}
		}
		if len(out.Projects.Items) > limit {
			out.Projects.Items = out.Projects.Items[:limit]
			out.Projects.HasMore = true
			out.Projects.NextCursor = cursor(cursorScope(ctx, 0, "client_projects"), visibleIDs[limit-1])
		}
		return nil
	})
	if e != nil {
		return nil, httpError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}

// HTTPMiddleware applies the external boundary before any business handler,
// including downloads, counts, search, export and realtime entry points.
func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(p) >= 3 && p[0] == "entities" && p[2] == "test-cases" {
			transportaudit.OmitConfidentialRequest(r.Context())
		}
		if len(p) == 0 || p[0] == "" || p[0] == "health" || p[0] == "openapi.json" || p[0] == "docs" {
			next.ServeHTTP(w, r)
			return
		}
		if m.verifier == nil || m.writer() == nil || m.clients == nil {
			respx.WriteError(w, r, 503, ci.Unavailable, 0)
			return
		}
		claims, e := m.verifier.Authenticate(r.Context(), r.Header.Get("Authorization"), r.Header.Get("Cookie"))
		if e != nil || claims == nil {
			next.ServeHTTP(w, r)
			return
		}
		// Delegated OAuth tokens have no project authority. Let the route's
		// registrar enforce its token-origin or session-only guard before lookup.
		if claims.ClientID != "" {
			next.ServeHTTP(w, r)
			return
		}
		// Authentication/account endpoints retain their own self-service guards.
		if p[0] == "me" || p[0] == "sessions" || p[0] == "auth" || p[0] == "oauth" || p[0] == "oidc" || p[0] == ".well-known" || (len(p) >= 2 && p[0] == "principals" && p[1] == claims.PrincipalID.String()) {
			next.ServeHTTP(w, r)
			return
		}
		var external bool
		channelAllowed := true
		route := p
		tenant := guid.ID(0)
		// EntityOwnerResolver reads through its own pool, so resolve it before
		// holding a transaction connection (including single-connection pools).
		e = func() error {
			if len(p) >= 2 && p[0] == "tenants" {
				var err error
				tenant, err = guid.RequireID(p[1])
				if err != nil {
					return ErrNotFound
				}
				route = p[2:]
			} else if len(p) >= 2 && p[0] == "entities" {
				id, err := guid.RequireID(p[1])
				if err != nil {
					return ErrNotFound
				}
				owner, found, err := m.owner(r.Context(), id)
				if err != nil {
					return err
				}
				if !found {
					return ErrNotFound
				}
				tenant = owner.TenantID
			}
			return nil
		}()
		if e == nil {
			e = m.writer().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
				if len(p) >= 2 && p[0] == "channels" {
					id, err := guid.RequireID(p[1])
					if err != nil {
						return ErrNotFound
					}
					tenant, channelAllowed, err = m.clients.ChannelOn(r.Context(), tx, id, claims.PrincipalID)
					if err != nil {
						return err
					}
				}
				var err error
				external, err = m.clients.IsExternalOn(r.Context(), tx, tenant, claims.PrincipalID)
				return err
			})
		}
		if e != nil {
			if errors.Is(e, ErrNotFound) || errors.Is(e, gorm.ErrRecordNotFound) {
				respx.WriteError(w, r, 404, ci.NotFound, 0)
			} else {
				respx.WriteError(w, r, 503, ci.Unavailable, 0)
			}
			return
		}
		if external {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Add("Vary", "Authorization")
			w.Header().Add("Vary", "Cookie")
		}
		if external && (!channelAllowed || !externalRouteAllowed(r.Method, route, claims.PrincipalID)) {
			respx.WriteError(w, r, 404, ci.NotFound, 0)
			return
		}
		if external {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func externalRouteAllowed(method string, p []string, actor guid.ID) bool {
	if rubricRoute(method, p) {
		return true
	}
	if len(p) == 1 && p[0] == "client-access" {
		return method == "GET"
	}
	// Own IAM permission/menu endpoints contain no project payload; the frontend narrows their codes.
	if method == "GET" && len(p) == 3 && p[0] == "principals" && p[1] == actor.String() && (p[2] == "permissions" || p[2] == "menus") {
		return true
	}
	if method == "GET" && len(p) == 5 && p[0] == "entities" && p[2] == "principals" && p[3] == actor.String() && p[4] == "permissions" {
		return true
	}
	if len(p) == 7 && p[0] == "entities" && p[2] == "delivery-packages" && p[4] == "items" && validPackageItemID(p[5]) && p[6] == "comments" {
		_, err := guid.RequireID(p[3])
		return err == nil && (method == "GET" || method == "POST")
	}
	if len(p) >= 3 && p[0] == "entities" {
		if len(p) > 3 {
			if _, err := guid.RequireID(p[3]); err != nil {
				return false
			}
		}
		switch p[2] {
		case "client-clarifications":
			return (method == "GET" && (len(p) == 3 || len(p) == 4)) || (method == "POST" && len(p) == 5 && p[4] == "answers")
		case "client-access", "client-pending":
			return len(p) == 3 && method == "GET"
		case "requirement-sheets", "client-changes", "client-issues", "delivery-packages", "artifact-previews", "artifact-comments":
			if method == "GET" {
				return (p[2] == "delivery-packages" && ((len(p) == 5 && p[4] == "bundle") || (len(p) == 6 && p[4] == "bundle" && p[5] == "content"))) || len(p) == 3 || len(p) == 4 || (len(p) == 5 && p[2] == "artifact-previews" && p[4] == "content") || (len(p) == 7 && p[2] == "delivery-packages" && p[4] == "artifacts" && p[6] == "content")
			}
			if method == "POST" {
				return (len(p) == 3 && (p[2] == "client-changes" || p[2] == "client-issues" || p[2] == "artifact-comments")) || (len(p) == 5 && ((p[2] == "requirement-sheets" && p[4] == "signoffs") || (p[2] == "client-changes" && p[4] == "actions") || (p[2] == "delivery-packages" && p[4] == "receipts")))
			}
			return false
		case "channels":
			return len(p) == 3 && method == "GET"
		}
		return false
	}
	if len(p) >= 3 && p[0] == "channels" {
		if len(p) == 3 {
			return (p[2] == "messages" && (method == "GET" || method == "POST")) || (p[2] == "members" && method == "GET") || (p[2] == "read-marker" && method == "PUT")
		}
		if len(p) == 7 && p[2] == "messages" && p[4] == "attachments" && p[6] == "content" {
			return method == "GET"
		}
		if len(p) == 5 && p[2] == "messages" && p[4] == "saved" {
			return method == "PUT"
		}
	}
	return false
}
