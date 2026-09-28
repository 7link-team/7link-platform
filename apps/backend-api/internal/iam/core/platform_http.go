package core

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

// PlatformAdministratorView is the API projection of one principal's platform
// authorization.
type PlatformAdministratorView struct {
	PrincipalID       string    `json:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	FullAdministrator bool      `json:"full_administrator" doc:"true when the principal holds every declared platform permission"`
	Permissions       []string  `json:"permissions" doc:"explicit platform permission codes; empty for a full administrator"`
	CreatedAt         time.Time `json:"created_at"`
}

type putPlatformAdministratorInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		FullAdministrator bool     `json:"full_administrator" doc:"grant every declared platform permission"`
		Permissions       []string `json:"permissions,omitempty" maxItems:"32" doc:"explicit platform permission codes; must be empty when full_administrator is true"`
	}
}

type platformPrincipalInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// listPlatformAdministratorsInput bounds the listing by default. The set is
// platform-wide rather than tenant-scoped, so nothing else narrows it.
type listPlatformAdministratorsInput struct {
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

func registerPlatformHTTP(a huma.API, svc *Service, registrar *authz.Registrar) {
	authz.RegisterAuthenticated(registrar, a, huma.Operation{OperationID: "iam-system-my-permissions", Method: http.MethodGet, Path: "/system/permissions", Summary: "Return my system permission codes", Tags: []string{"system"}}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]string], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil {
			return nil, huma.Error401Unauthorized(respx.Unauthorized)
		}
		if view := claims.AccountView; view != nil && (view.ScopeType != "system" || view.ScopeID != systemScopeID) {
			return nil, huma.Error403Forbidden(respx.Forbidden)
		}
		codes, err := svc.EffectivePermissions(ctx, 0, claims.PrincipalID)
		if err != nil {
			return nil, platformAPIError("effective system permissions", err)
		}
		return respx.OK(ctx, codes), nil
	})
	authz.RegisterPlatform(registrar, a, huma.Operation{
		OperationID: "iam-platform-permission-catalog", Method: http.MethodGet, Path: "/system/permission-catalog",
		Summary: "List declared system permissions", Tags: []string{"system"},
	}, authz.Guard{Resource: "platform", Verb: "administer"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]authz.Action], error) {
		return respx.OK(ctx, svc.PlatformPermissionCatalog(ctx)), nil
	})

	authz.RegisterPlatform(registrar, a, huma.Operation{
		OperationID: "iam-list-platform-administrators", Method: http.MethodGet, Path: "/system/administrators",
		Summary: "List system administrators and their permissions", Tags: []string{"system"},
	}, authz.Guard{Resource: "platform", Verb: "administer"}, func(ctx context.Context, in *listPlatformAdministratorsInput) (*respx.Body[[]PlatformAdministratorView], error) {
		administrators, total, err := svc.ListPlatformAdministratorsPage(ctx, in.Offset, in.Limit)
		if err != nil {
			return nil, platformAPIError("list platform administrators", err)
		}
		return respx.List(ctx, platformAdministratorsFromDomain(administrators), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})

	authz.RegisterPlatform(registrar, a, huma.Operation{
		OperationID: "iam-put-platform-administrator", Method: http.MethodPut, Path: "/system/administrators/{principal_id}",
		Summary: "Grant or restrict system authorization for an account", Tags: []string{"system"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "platform", Verb: "administer"}, func(ctx context.Context, in *putPlatformAdministratorInput) (*respx.Body[PlatformAdministratorView], error) {
		principalID, err := guid.RequireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		administrator, err := svc.SetPlatformAdministrator(ctx, principalID, in.Body.FullAdministrator, in.Body.Permissions)
		if err != nil {
			return nil, platformAPIError("put platform administrator", err)
		}
		return respx.OK(ctx, platformAdministratorFromDomain(administrator)), nil
	})

	authz.RegisterPlatform(registrar, a, huma.Operation{
		OperationID: "iam-delete-platform-administrator", Method: http.MethodDelete, Path: "/system/administrators/{principal_id}",
		Summary: "Revoke system authorization for an account", Tags: []string{"system"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "platform", Verb: "administer"}, func(ctx context.Context, in *platformPrincipalInput) (*respx.Body[MutationResult], error) {
		principalID, err := guid.RequireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		changed, err := svc.DeletePlatformAdministrator(ctx, principalID)
		if err != nil {
			return nil, platformAPIError("delete platform administrator", err)
		}
		return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
	})
}

// platformAPIError maps platform authorization failures before delegating to
// the shared IAM error contract.
func platformAPIError(operation string, err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	switch {
	case errors.Is(err, ErrPlatformAdministratorNotFound):
		return huma.Error404NotFound(corei18n.PlatformAdministratorNotFound)
	case errors.Is(err, ErrLastPlatformAdministrator):
		return huma.Error409Conflict(corei18n.LastPlatformAdministrator)
	case errors.Is(err, ErrPlatformPermissionScope):
		return huma.Error422UnprocessableEntity(corei18n.PlatformPermissionScope)
	default:
		return apiError(operation, err)
	}
}

func platformAdministratorFromDomain(administrator PlatformAdministrator) PlatformAdministratorView {
	permissions := administrator.Permissions
	if permissions == nil {
		permissions = []string{}
	}
	return PlatformAdministratorView{
		PrincipalID: administrator.PrincipalID.String(), FullAdministrator: administrator.FullAdministrator,
		Permissions: permissions, CreatedAt: administrator.CreatedAt,
	}
}

func platformAdministratorsFromDomain(administrators []PlatformAdministrator) []PlatformAdministratorView {
	result := make([]PlatformAdministratorView, 0, len(administrators))
	for _, administrator := range administrators {
		result = append(result, platformAdministratorFromDomain(administrator))
	}
	return result
}
