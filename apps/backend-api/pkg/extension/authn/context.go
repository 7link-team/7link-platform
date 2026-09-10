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

func TenantIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.TenantID
}

func EntityIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.EntityID
}

func PrincipalIDFromContext(ctx context.Context) guid.ID {
	claims, _ := FromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.PrincipalID
}
