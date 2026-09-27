package authn

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type accountViewCreateInput struct {
	Cookie        string `header:"Cookie" hidden:"true"`
	Authorization string `header:"Authorization" hidden:"true"`
	Origin        string `header:"Origin"`
	Body          AccountViewRequest
}

type accountViewRevokeInput struct {
	Cookie        string `header:"Cookie" hidden:"true"`
	Authorization string `header:"Authorization" hidden:"true"`
	Origin        string `header:"Origin"`
	ID            string `path:"account_view_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// Admission is deliberately enumerated: adding an authn route cannot expose a
// security mutation or credential conversion to either account-view mode.
func registerAccountViewBoundary(api huma.API) {
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if !strings.HasPrefix(ctx.Operation().OperationID, "authn-") {
			next(ctx)
			return
		}
		claims, _ := authnext.FromContext(ctx.Context())
		scheme, raw, bearer := strings.Cut(strings.TrimSpace(ctx.Header("Authorization")), " ")
		view := bearer && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(raw, "avt_")
		view = view || (claims != nil && claims.AccountView != nil)
		if view {
			allowed := false
			if ctx.Method() == http.MethodGet || ctx.Method() == http.MethodHead {
				switch ctx.Operation().OperationID {
				case "authn-me", "authn-get-session", "authn-list-sessions", "authn-get-mfa", "authn-list-passkeys", "authn-list-personal-tokens":
					allowed = true
				}
			}
			if !allowed {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, respx.Forbidden)
				return
			}
		}
		next(ctx)
	})
}

func registerAccountViewHTTP(api huma.API, service *Service) {
	registrar := authz.NewAuthenticationOnlyRegistrar(service)
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{
		OperationID: "authn-create-account-view", Method: http.MethodPost, Path: "/account-views",
		Summary: "Open an administrator account view", Tags: []string{"account"}, Errors: []int{401, 403, 422, 503},
	}, func(ctx context.Context, in *accountViewCreateInput) (*respx.Body[AccountViewCreated], error) {
		if err := service.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		created, err := service.CreateAccountView(ctx, in.Cookie, in.Body)
		if err != nil {
			return nil, accountViewHTTPError(ctx, err)
		}
		return respx.OK(ctx, created), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{
		OperationID: "authn-delete-account-view", Method: http.MethodDelete, Path: "/account-views/{account_view_id}",
		Summary: "Close an administrator account view", Tags: []string{"account"}, Errors: []int{401, 403, 422, 503},
	}, func(ctx context.Context, in *accountViewRevokeInput) (*respx.Body[map[string]bool], error) {
		if err := service.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		id, err := guid.Parse(in.ID)
		if err != nil || id <= 0 || id.String() != in.ID {
			return nil, huma.Error422UnprocessableEntity("invalid_account_view_id")
		}
		if err := service.RevokeAccountView(ctx, in.Cookie, id); err != nil {
			return nil, accountViewHTTPError(ctx, err)
		}
		return respx.OK(ctx, map[string]bool{"revoked": true}), nil
	})
}

// An unavailable dependency is logged here, where the cause is still known; the
// response deliberately carries only the stable code.
func accountViewHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, authnext.ErrUnavailable) {
		if ctx.Err() != nil {
			slog.DebugContext(ctx, "account view abandoned by the caller", "error", err)
		} else {
			slog.ErrorContext(ctx, "account view unavailable", "error", err)
		}
		return huma.Error503ServiceUnavailable("authentication_unavailable")
	}
	return authenticationError(err)
}
