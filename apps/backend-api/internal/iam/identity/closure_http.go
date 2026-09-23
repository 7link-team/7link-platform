package identity

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/iam/core"
	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type ClosureBrowserInput struct {
	Cookie        string `header:"Cookie"`
	Authorization string `header:"Authorization" hidden:"true"`
	Origin        string `header:"Origin"`
}
type closureSubmitInput struct {
	ClosureBrowserInput
	Body struct {
		Proof   string `json:"proof" minLength:"1" maxLength:"1024"`
		Version int64  `json:"version" minimum:"0"`
	}
}
type closureCancelInput struct {
	ClosureBrowserInput
	Body struct {
		Version int64 `json:"version" minimum:"1"`
	}
}
type closureUpdateInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		Version int64  `json:"version" minimum:"1"`
		Status  string `json:"status" enum:"processing,rejected"`
	}
}
type closureListInput struct {
	Status string `query:"status" enum:"pending,processing,canceled,rejected,completed"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type closurePage struct {
	Items []ClosureRequest `json:"items"`
	Total int64            `json:"total"`
}

func registerClosureHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-my-closure-request", Method: http.MethodGet, Path: "/me/closure-request", Summary: "Read my global account closure request", Tags: []string{"account"}, Errors: []int{401, 503}}, func(ctx context.Context, in *ClosureBrowserInput) (*respx.Body[ClosureRequest], error) {
		id, err := closurePrincipal(ctx, in.Cookie, in.Authorization)
		if err != nil {
			return nil, err
		}
		result, err := s.GetClosureRequest(ctx, id)
		if err != nil {
			return nil, closureError(err)
		}
		return respx.OK(ctx, result), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-submit-closure-request", Method: http.MethodPost, Path: "/me/closure-request", Summary: "Request global account closure without changing retention or access", Tags: []string{"account"}, Errors: []int{401, 403, 409, 503}}, func(ctx context.Context, in *closureSubmitInput) (*respx.Body[ClosureRequest], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodPost)
		if err != nil {
			return nil, err
		}
		result, err := s.ChangeClosureRequest(ctx, id, in.Body.Version, "pending", in.Cookie, in.Body.Proof)
		if err != nil {
			return nil, closureError(err)
		}
		return respx.OK(ctx, result), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "identity-cancel-closure-request", Method: http.MethodDelete, Path: "/me/closure-request", Summary: "Cancel my pending account closure request", Tags: []string{"account"}, Errors: []int{401, 403, 409, 503}}, func(ctx context.Context, in *closureCancelInput) (*respx.Body[ClosureRequest], error) {
		id, err := s.closureMutationPrincipal(ctx, in.ClosureBrowserInput, http.MethodDelete)
		if err != nil {
			return nil, err
		}
		result, err := s.ChangeClosureRequest(ctx, id, in.Body.Version, "canceled", in.Cookie, "")
		if err != nil {
			return nil, closureError(err)
		}
		return respx.OK(ctx, result), nil
	})
	guard := authz.Guard{Resource: "platform", Verb: "administer"}
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "identity-update-closure-request", Method: http.MethodPatch, Path: "/platform/principals/{principal_id}/closure-request", Summary: "Move a closure request to processing or rejected", Tags: []string{"platform"}, Errors: []int{404, 409, 503}}, guard, func(ctx context.Context, in *closureUpdateInput) (*respx.Body[ClosureRequest], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		var result ClosureRequest
		if in.Body.Status == "processing" {
			result, err = s.ProcessClosureRequest(ctx, id, in.Body.Version)
		} else {
			result, err = s.ChangeClosureRequest(ctx, id, in.Body.Version, "rejected", "", "")
		}
		if err != nil {
			return nil, closureError(err)
		}
		return respx.OK(ctx, result), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{OperationID: "identity-list-closure-requests", Method: http.MethodGet, Path: "/platform/closure-requests", Summary: "Track account closure requests and derived erasure completion", Tags: []string{"platform"}}, guard, func(ctx context.Context, in *closureListInput) (*respx.Body[closurePage], error) {
		items, total, err := s.ListClosureRequests(ctx, in.Status, in.Offset, in.Limit)
		if err != nil {
			return nil, closureError(err)
		}
		return respx.OK(ctx, closurePage{Items: items, Total: total}), nil
	})
}

func closurePrincipal(ctx context.Context, cookie, authorization string) (guid.ID, error) {
	claims, ok := authnext.FromContext(ctx)
	if authorization != "" || cookie == "" || !ok || claims == nil || claims.PrincipalID <= 0 || claims.SubjectType != authnext.SubjectTypePrincipal {
		return 0, huma.Error401Unauthorized("respx.unauthorized")
	}
	return claims.PrincipalID, nil
}

func (s *Service) closureMutationPrincipal(ctx context.Context, in ClosureBrowserInput, method string) (guid.ID, error) {
	id, err := closurePrincipal(ctx, in.Cookie, in.Authorization)
	if err != nil {
		return 0, err
	}
	if s == nil || s.privacyBrowser.ValidateCSRF == nil {
		return 0, huma.Error503ServiceUnavailable(identityi18n.IdentityUnavailable)
	}
	if err := s.privacyBrowser.ValidateCSRF(method, in.Origin, in.Cookie); err != nil {
		return 0, huma.Error403Forbidden("respx.forbidden")
	}
	return id, nil
}

func closureError(err error) error {
	if errors.Is(err, core.ErrPrincipalHasPlatformAccess) {
		return huma.Error409Conflict(identityi18n.PlatformAccessBlocksClosure)
	}
	if errors.Is(err, ErrClosureConflict) {
		return huma.Error409Conflict(identityi18n.ClosureConflict)
	}
	if errors.Is(err, authnext.ErrInvalidCredentials) || errors.Is(err, authnext.ErrInvalidToken) {
		return huma.Error403Forbidden("respx.forbidden")
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, authnext.ErrUnavailable) {
		return huma.Error503ServiceUnavailable(identityi18n.IdentityUnavailable)
	}
	return identityError(err)
}
