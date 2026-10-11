package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type deviceAuthorizationOutput struct {
	Status int `status:""`
	Body   DeviceAuthorization
}

type userCodeInput struct {
	UserCode string `query:"user_code" minLength:"1" maxLength:"64"`
}

type deviceDecisionInput struct {
	Body struct {
		UserCode string `json:"user_code" minLength:"1" maxLength:"64"`
		Approve  bool   `json:"approve"`
	}
}

type exchangeGrantListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type exchangeGrantCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		ClientID      string `json:"client_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		SubjectPrefix string `json:"subject_prefix" minLength:"1" maxLength:"128" doc:"Principal identifier, or a prefix ending in * to permit a range; a bare * is refused"`
		Scope         string `json:"scope" minLength:"1" maxLength:"2048" doc:"Scopes this delegation may ask for"`
		Status        string `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

type exchangeGrantIDInput struct {
	GrantID string `path:"grant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

// registerDeviceHTTP mounts the device grant's two public endpoints and the
// tenant-administered exchange grants.
//
// The device endpoint is public because a device has no credential to present;
// its protection is that the client must have declared the grant. The approval
// endpoints are not public: somebody signed in has to approve, and the token the
// device collects is issued for them.
func registerDeviceHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	start := huma.Operation{
		OperationID: "oauth-device-authorization", Method: http.MethodPost, Path: "/oauth/device_authorization",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitOAuth),
		Summary:  "Start a device authorization request", SkipValidateParams: true,
		Tags: []string{"oauth"}, DefaultStatus: http.StatusOK,
	}
	authz.RegisterPublic(api, start, func(ctx context.Context, in *formInput) (*deviceAuthorizationOutput, error) {
		form, err := url.ParseQuery(string(in.RawBody))
		if err != nil {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		answer, err := service.StartDeviceAuthorization(ctx, form.Get("client_id"), form.Get("scope"))
		if err != nil {
			if errors.Is(err, errInvalidClient) {
				return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_client")
			}
			if errors.Is(err, ErrInvalidRequest) {
				return nil, oauthError(ctx, http.StatusBadRequest, "invalid_scope")
			}
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		return &deviceAuthorizationOutput{Status: http.StatusOK, Body: answer}, nil
	})
	documentOAuthErrors(api, api.OpenAPI().Paths["/oauth/device_authorization"].Post,
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError)
	documentOAuthForm[deviceAuthorizationForm](api, "/oauth/device_authorization")

	// Reading a pending request requires a signed-in person: the page that shows
	// what is being approved is shown to whoever will approve it.
	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-device-request", Method: http.MethodGet, Path: "/oauth/device-requests",
		Summary: "Read a pending device authorization request by its user code",
		Tags:    []string{"oauth"}, Errors: []int{http.StatusNotFound},
	}, authz.Guard{Resource: "oauth_device_request", Verb: "view"},
		func(ctx context.Context, in *userCodeInput) (*respx.Body[DeviceRequest], error) {
			item, err := service.LookupDeviceRequest(ctx, authnext.TenantIDFromContext(ctx), in.UserCode)
			if err != nil {
				return nil, deviceError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-device-decision", Method: http.MethodPost, Path: "/oauth/device-requests/decisions",
		Summary: "Approve or refuse a device authorization request",
		Tags:    []string{"oauth"}, Errors: []int{http.StatusNotFound},
	}, authz.Guard{Resource: "oauth_device_request", Verb: "approve"},
		func(ctx context.Context, in *deviceDecisionInput) (*respx.Body[map[string]string], error) {
			claims, ok := authnext.FromContext(ctx)
			if !ok || claims == nil || claims.PrincipalID.Zero() {
				return nil, deviceError(ErrDeviceRequestNotFound)
			}
			// The decision carries the approver's own assurance, so the token the
			// device collects can never claim a stronger authentication than the
			// person who approved it actually had.
			if err := service.DecideDeviceRequest(
				ctx, authnext.TenantIDFromContext(ctx), in.Body.UserCode, claims.PrincipalID, in.Body.Approve,
				claims.AuthTime, claims.ACR, joinWords(claims.AMR),
			); err != nil {
				return nil, deviceError(err)
			}
			decision := "denied"
			if in.Body.Approve {
				decision = "approved"
			}
			return respx.OK(ctx, map[string]string{"status": decision}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-list-exchange-grants", Method: http.MethodGet, Path: "/oauth-exchange-grants",
		Summary: "List token exchange grants", Tags: []string{"oauth"},
	}, authz.Guard{Resource: "oauth_exchange_grant", Verb: "view"},
		func(ctx context.Context, in *exchangeGrantListInput) (*respx.Body[[]ExchangeGrant], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			items, err := service.ListExchangeGrants(ctx, tenantID)
			if err != nil {
				return nil, deviceError(err)
			}
			return respx.OK(ctx, items), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-create-exchange-grant", Method: http.MethodPost, Path: "/oauth-exchange-grants",
		Summary: "Permit one client to act as a subject", Tags: []string{"oauth"},
		DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "oauth_exchange_grant", Verb: "create"},
		func(ctx context.Context, in *exchangeGrantCreateInput) (*respx.Body[ExchangeGrant], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			clientID, err := guid.RequireID(in.Body.ClientID)
			if err != nil {
				return nil, err
			}
			item, err := service.CreateExchangeGrant(ctx, tenantID, ExchangeGrantInput{
				ClientID: clientID, SubjectPrefix: in.Body.SubjectPrefix,
				Scope: in.Body.Scope, Status: in.Body.Status,
			})
			if err != nil {
				return nil, deviceError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "oauth-delete-exchange-grant", Method: http.MethodDelete,
		Path:    "/oauth-exchange-grants/{grant_id}",
		Summary: "Withdraw a token exchange grant", Tags: []string{"oauth"},
		Errors: []int{http.StatusNotFound},
	}, "grant_id", service.resolveExchangeGrantOwner,
		authz.Guard{Resource: "oauth_exchange_grant", Verb: "delete"},
		func(ctx context.Context, in *exchangeGrantIDInput) (*respx.Body[map[string]bool], error) {
			tenantID := authnext.TenantIDFromContext(ctx)
			id, err := guid.RequireID(in.GrantID)
			if err != nil {
				return nil, err
			}
			if err := service.DeleteExchangeGrant(ctx, tenantID, id); err != nil {
				return nil, deviceError(err)
			}
			return respx.OK(ctx, map[string]bool{"deleted": true}), nil
		})
}

// resolveExchangeGrantOwner tells the authorization layer which tenant a grant
// belongs to, so one tenant cannot withdraw another's delegation by its id.
func (s *Service) resolveExchangeGrantOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if s == nil {
		return authz.ResourceOwner{}, false, ErrExchangeGrantMissing
	}
	tenantID, err := s.repo.exchangeGrantOwner(ctx, id)
	if errors.Is(err, ErrExchangeGrantMissing) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{TenantID: tenantID}, true, nil
}

func deviceError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	switch {
	case errors.Is(err, ErrDeviceRequestNotFound):
		return huma.Error404NotFound(oauthi18n.DeviceRequestMissing)
	case errors.Is(err, ErrExchangeGrantMissing):
		return huma.Error404NotFound(oauthi18n.ExchangeGrantMissing)
	case errors.Is(err, ErrExchangeGrantExists):
		return huma.Error409Conflict(oauthi18n.ExchangeGrantExists)
	case errors.Is(err, ErrInvalidExchangeGrant):
		return huma.Error422UnprocessableEntity(oauthi18n.ExchangeGrantInvalid)
	default:
		return huma.Error500InternalServerError(oauthi18n.Unavailable)
	}
}

// joinWords renders the methods an assurance carries back into the stored form.
func joinWords(values []string) string {
	return normalizeWords(strings.Join(values, " "))
}
