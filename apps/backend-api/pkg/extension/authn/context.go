// Package authn stores the authenticated local subject in request context.
package authn

import (
	"context"

	"7link.com/platform/internal/infra/guid"
)

type contextKey struct{}

type personalTokenScopeKey struct{}

// PersonalTokenScope is supplied by a permission-enforcing route, never by a
// client. Authentication-only and platform routes do not accept personal tokens.
type PersonalTokenScope struct {
	TenantID   guid.ID
	Permission string
}

// WithPersonalTokenScope binds authentication to the route's actual selector.
func WithPersonalTokenScope(ctx context.Context, tenantID guid.ID, permission string) context.Context {
	return context.WithValue(ctx, personalTokenScopeKey{}, PersonalTokenScope{TenantID: tenantID, Permission: permission})
}

// PersonalTokenScopeFromContext returns the permission enforced by this route.
func PersonalTokenScopeFromContext(ctx context.Context) (PersonalTokenScope, bool) {
	scope, ok := ctx.Value(personalTokenScopeKey{}).(PersonalTokenScope)
	return scope, ok && !scope.TenantID.Zero() && scope.Permission != ""
}

// WithClaims stores verified claims in ctx.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

// FromContext returns verified claims from ctx.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(contextKey{}).(*Claims)
	return claims, ok
}

// SubjectFromContext returns the verified token subject. Callers that require
// a human principal must also check Claims.SubjectType.
func SubjectFromContext(ctx context.Context) (string, bool) {
	claims, ok := FromContext(ctx)
	if !ok {
		return "", false
	}
	return claims.Subject, true
}

// TenantIDFromContext returns the organization root the request is authorized
// in: a tenant on tenant routes, or an operating platform on operating-platform
// routes, whose guard rewrites Claims.TenantID to the platform. It is zero on
// routes that bind no root. Callers that must tell the two kinds apart resolve
// the root's kind rather than assume a tenant.
func TenantIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.TenantID
}

// EntityIDFromContext returns the entity inside the organization root the
// request is authorized for, or zero when the route binds none (operating-
// platform routes always clear it).
func EntityIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.EntityID
}

// PrincipalIDFromContext returns the principal whose authority the request
// carries. Under an account view that is the viewed account, not the operator
// who opened the view; audit attribution uses ActorIDFromContext instead.
func PrincipalIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.PrincipalID
}

// ActorIDFromContext identifies who performed an operation, including account views.
// Resource ownership and permission checks continue to use PrincipalIDFromContext.
func ActorIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	if claims.AccountView != nil {
		return claims.AccountView.ActorID
	}
	return claims.PrincipalID
}
