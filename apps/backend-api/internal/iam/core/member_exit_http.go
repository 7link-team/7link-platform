package core

import (
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"context"
	"errors"
	"net/http"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type myMembershipInput struct {
	Cookie        string `header:"Cookie"`
	Authorization string `header:"Authorization" hidden:"true"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type myMembershipPage struct {
	Items []MyMembership `json:"items"`
	Total int64          `json:"total"`
}
type leaveMembershipInput struct {
	Cookie        string `header:"Cookie"`
	Authorization string `header:"Authorization" hidden:"true"`
	Origin        string `header:"Origin"`
	TenantID      string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body          struct {
		Proof string `json:"proof" minLength:"1" maxLength:"1024"`
	}
}

func registerMemberExitHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "iam-my-memberships", Method: http.MethodGet, Path: "/iam/me/memberships", Summary: "List my active tenant memberships", Tags: []string{"iam"}, Security: authz.UserSecurity(), Errors: []int{401, 503}}, func(ctx context.Context, in *myMembershipInput) (*respx.Body[myMembershipPage], error) {
		claims, err := s.memberBrowser(ctx, in.Cookie, in.Authorization)
		if err != nil {
			return nil, err
		}
		items, total, err := s.MyMemberships(ctx, claims.PrincipalID, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list own memberships", err)
		}
		return respx.OK(ctx, myMembershipPage{Items: items, Total: total}), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "iam-leave-membership", Method: http.MethodPost, Path: "/iam/me/memberships/{tenant_id}/leave", Summary: "Leave one tenant while preserving my global account", Tags: []string{"iam"}, Security: authz.UserSecurity(), Errors: []int{401, 403, 404, 409, 503}}, func(ctx context.Context, in *leaveMembershipInput) (*respx.Body[map[string]string], error) {
		claims, err := s.memberBrowser(ctx, in.Cookie, in.Authorization)
		if err != nil {
			return nil, err
		}
		if s.privacyBrowser.ValidateCSRF == nil {
			return nil, huma.Error503ServiceUnavailable(corei18n.Unavailable)
		}
		if err := s.privacyBrowser.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie); err != nil {
			return nil, huma.Error403Forbidden("respx.forbidden")
		}
		tenant, err := requireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		_, err = s.LeaveTenant(ctx, claims.PrincipalID, tenant, in.Cookie, in.Body.Proof)
		if errors.Is(err, authnext.ErrInvalidCredentials) || errors.Is(err, authnext.ErrInvalidToken) {
			return nil, huma.Error403Forbidden("respx.forbidden")
		}
		if errors.Is(err, ErrUnavailable) || errors.Is(err, authnext.ErrUnavailable) {
			return nil, huma.Error503ServiceUnavailable(corei18n.Unavailable)
		}
		if err != nil {
			return nil, apiError("leave membership", err)
		}
		return respx.OK(ctx, map[string]string{"status": "disabled"}), nil
	})
}

func (s *Service) memberBrowser(ctx context.Context, cookie, authorization string) (*authnext.Claims, error) {
	if authorization != "" || cookie == "" {
		return nil, huma.Error401Unauthorized("respx.unauthorized")
	}
	claims, ok := authnext.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID <= 0 || claims.SubjectType != authnext.SubjectTypePrincipal {
		return nil, huma.Error401Unauthorized("respx.unauthorized")
	}
	return claims, nil
}
