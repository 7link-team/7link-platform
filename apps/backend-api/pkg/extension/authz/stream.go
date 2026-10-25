package authz

import (
	"context"
	"errors"
	"io"
	"net/http"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterPrincipalPlatformInboxStream exposes only the caller's system inbox.
// Content remains protected by the notification owner's per-record permission.
func RegisterPrincipalPlatformInboxStream(r *Registrar, api huma.API, op huma.Operation, handler func(huma.Context)) {
	markPrincipalScope(&op)
	r.Authenticate(api, &op)
	preparePrincipalScope(api, &op)
	op.Middlewares = append(op.Middlewares, r.platformInboxMiddleware(api))
	api.OpenAPI().AddOperation(&op)
	api.Adapter().Handle(&op, api.Middlewares().Handler(op.Middlewares.Handler(handler)))
}

func (r *Registrar) platformInboxMiddleware(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		claims, ok := authn.FromContext(ctx.Context())
		if !ok || claims == nil || claims.PrincipalID.Zero() || claims.AccountView != nil {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
			return
		}
		if r.checker == nil {
			_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable, respx.AuthorizationUnavailable)
			return
		}
		for _, permission := range []string{"system_job_manage", "system_event_manage"} {
			allowed, err := r.checker.CheckPlatform(ctx.Context(), permission, claims.PrincipalID)
			if err != nil {
				_ = huma.WriteErr(api, ctx, http.StatusServiceUnavailable, respx.AuthorizationUnavailable)
				return
			}
			if allowed {
				next(ctx)
				return
			}
		}
		_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
	}
}

// RecheckPrincipalPlatformInboxStream checks the original credentials and current
// system grants again, without writing a second response after SSE has started.
func (r *Registrar) RecheckPrincipalPlatformInboxStream(api huma.API, original huma.Context) (context.Context, error) {
	if r == nil || r.verifier == nil {
		return nil, errors.New("platform inbox authentication unavailable")
	}
	sink := &streamCheckContext{streamContext: original}
	var checked context.Context
	r.authenticatedOnly(api)(sink, func(ctx huma.Context) {
		r.platformInboxMiddleware(api)(ctx, func(ctx huma.Context) { checked = ctx.Context() })
	})
	if checked == nil {
		return nil, errors.New("platform inbox credentials or authority revoked")
	}
	return checked, nil
}

// RegisterTenantMemberStream is the raw transport equivalent of
// RegisterTenantMember, including account-view, CSRF and membership checks.
func RegisterTenantMemberStream(r *Registrar, api huma.API, op huma.Operation, handler func(huma.Context)) {
	if r == nil || api == nil || handler == nil || op.OperationID == "" {
		panic("tenant stream requires registrar, api, operation id, and handler")
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[tenantMemberMetadataKey] = true
	r.prepareAuthenticated(api, &op, true)
	prepareTenantPath(&op)
	requireResourceTag(&op)
	if r.verifier != nil {
		op.Middlewares = append(op.Middlewares, r.middleware(api, nil))
	}
	preparePrincipalScope(api, &op)
	api.OpenAPI().AddOperation(&op)
	api.Adapter().Handle(&op, api.Middlewares().Handler(op.Middlewares.Handler(handler)))
}

// RecheckTenantMemberStream reuses the complete authentication/member guard
// against the original request headers. Rejections go to a sink: after SSE
// starts, callers must send a protocol frame instead of a second HTTP response.
func (r *Registrar) RecheckTenantMemberStream(api huma.API, original huma.Context) (context.Context, error) {
	if r == nil || r.verifier == nil || r.members == nil {
		return nil, errors.New("tenant stream authentication unavailable")
	}
	sink := &streamCheckContext{streamContext: original}
	var checked context.Context
	r.middleware(api, nil)(sink, func(ctx huma.Context) { checked = ctx.Context() })
	if checked == nil {
		return nil, errors.New("tenant stream credentials or membership revoked")
	}
	return checked, nil
}

type streamContext interface{ huma.Context }
type streamCheckContext struct {
	streamContext
	status int
}

func (c *streamCheckContext) SetStatus(status int)      { c.status = status }
func (c *streamCheckContext) Status() int               { return c.status }
func (*streamCheckContext) SetHeader(string, string)    {}
func (*streamCheckContext) AppendHeader(string, string) {}
func (*streamCheckContext) BodyWriter() io.Writer       { return io.Discard }
