package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"7link.com/platform/internal/iam/core"
	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

func requireID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil || id.Zero() {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func principalRef(tenantValue, idValue string) (guid.ID, guid.ID, error) {
	tenantID, err := requireID(tenantValue)
	if err != nil {
		return 0, 0, err
	}
	id, err := requireID(idValue)
	if err != nil {
		return 0, 0, err
	}
	return tenantID, id, nil
}

type principalListInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Search   string `query:"search" maxLength:"200"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
}

type principalListData struct {
	Items []PrincipalView `json:"items"`
	Total int64           `json:"total"`
}

type principalIDInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	ID       string `path:"principal_id" maxLength:"64"`
}

type principalCreateInput struct {
	RawBody  []byte
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		LoginName   string `json:"login_name" minLength:"1" maxLength:"200"`
		Password    string `json:"password" minLength:"12" maxLength:"1024"`
		DisplayName string `json:"display_name,omitempty" maxLength:"128"`
		Email       string `json:"email,omitempty" maxLength:"320"`
	}
}

type principalUpdateInput struct {
	RawBody  []byte
	TenantID string `path:"tenant_id" maxLength:"128"`
	ID       string `path:"principal_id" maxLength:"64"`
	Body     struct {
		DisplayName *string `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
		Email       *string `json:"email,omitempty" maxLength:"320"`
	}
}

type serviceAccountListInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Search   string `query:"search" maxLength:"200"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset   int    `query:"offset" default:"0" minimum:"0"`
}

type serviceAccountListData struct {
	Items []ServiceAccount `json:"items"`
	Total int64            `json:"total"`
}

type serviceAccountIDInput struct {
	ID string `path:"service_account_id" maxLength:"64"`
}

type serviceAccountCreateInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		LoginName   string     `json:"login_name" minLength:"1" maxLength:"200"`
		DisplayName string     `json:"display_name,omitempty" maxLength:"128"`
		Description string     `json:"description,omitempty" maxLength:"1000"`
		ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	}
}

type serviceAccountReplaceInput struct {
	ID   string `path:"service_account_id" maxLength:"64"`
	Body struct {
		DisplayName string     `json:"display_name" minLength:"1" maxLength:"128"`
		Description string     `json:"description,omitempty" maxLength:"1000"`
		Status      string     `json:"status" enum:"active,disabled"`
		ExpiresAt   *time.Time `json:"expires_at,omitempty"`
		Version     int64      `json:"version" minimum:"1"`
	}
}

type serviceAccountDeleteInput struct {
	ID      string `path:"service_account_id" maxLength:"64"`
	Version int64  `query:"version" minimum:"1"`
}

type serviceAccountCredentialCreateInput struct {
	ID   string `path:"service_account_id" maxLength:"64"`
	Body struct {
		Name      string     `json:"name" minLength:"1" maxLength:"128"`
		Scopes    []string   `json:"scopes" minItems:"1" maxItems:"128"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
}

type serviceAccountCredentialIDInput struct {
	ID           string `path:"service_account_id" maxLength:"64"`
	CredentialID string `path:"credential_id" maxLength:"64"`
}

// RegisterHTTP publishes the identity module's principal and service-account
// operations. Suspension is modeled as a subresource so disabling and
// restoring a principal use HTTP methods instead of RPC-style verbs.
func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	registerAvatarHTTP(api, service, registrar)
	registerClosureHTTP(api, service, registrar)
	registerPrincipalHTTP(api, service, registrar)
	registerServiceAccountHTTP(api, service, registrar)
	registerPrincipalLifecycleHTTP(api, service, registrar)
	registerPlatformStatusHTTP(api, service, registrar)
}

func registerPrincipalHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-list-principals",
		Method:      http.MethodGet,
		Path:        "/principals",
		Summary:     "List local principals in the tenant",
		Tags:        []string{"directory"},
	}, authz.Guard{Resource: "user", Verb: "view"},
		func(ctx context.Context, in *principalListInput) (*respx.Body[principalListData], error) {
			tenantID, err := requireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err == nil {
				err = fields.RequireRead("login_name") // The endpoint orders by login name.
			}
			if err == nil && strings.TrimSpace(in.Search) != "" {
				err = fields.RequireRead("display_name", "email")
			}
			if err != nil {
				return nil, identityError(err)
			}
			items, total, err := service.List(ctx, tenantID, in.Search, in.Limit, in.Offset)
			if err != nil {
				return nil, identityError(err)
			}
			views := make([]PrincipalView, len(items))
			for i, item := range items {
				views[i] = profileView(item, fields)
			}
			return respx.OK(ctx, principalListData{Items: views, Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID:   "identity-create-principal",
		Method:        http.MethodPost,
		Path:          "/principals",
		Summary:       "Create a local principal and tenant membership",
		Tags:          []string{"directory"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "user", Verb: "create"},
		func(ctx context.Context, in *principalCreateInput) (*respx.Body[PrincipalView], error) {
			tenantID, err := requireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err == nil {
				err = requireProfileWrites(in.RawBody, fields, true)
			}
			if err != nil {
				return nil, identityError(err)
			}
			principal, err := service.Create(
				ctx, tenantID, in.Body.LoginName, in.Body.Password, in.Body.DisplayName, in.Body.Email,
			)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, profileView(principal, fields)), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-get-principal",
		Method:      http.MethodGet,
		Path:        "/principals/{principal_id}",
		Summary:     "Get a local principal in the tenant",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound},
	}, authz.Guard{Resource: "user", Verb: "view"},
		func(ctx context.Context, in *principalIDInput) (*respx.Body[PrincipalView], error) {
			tenantID, id, err := principalRef(in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err != nil {
				return nil, identityError(err)
			}
			principal, err := service.Get(ctx, tenantID, id)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, profileView(principal, fields)), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-update-principal",
		Method:      http.MethodPatch,
		Path:        "/principals/{principal_id}",
		Summary:     "Update a local principal profile",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "user", Verb: "update"},
		func(ctx context.Context, in *principalUpdateInput) (*respx.Body[PrincipalView], error) {
			tenantID, id, err := principalRef(in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err == nil {
				err = requireProfileWrites(in.RawBody, fields, false)
			}
			if err != nil {
				return nil, identityError(err)
			}
			principal, err := service.Update(ctx, tenantID, id, in.Body.DisplayName, in.Body.Email)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, profileView(principal, fields)), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-create-principal-suspension",
		Method:      http.MethodPut,
		Path:        "/principals/{principal_id}/suspension",
		Summary:     "Suspend a principal and revoke its sessions and refresh tokens",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, authz.Guard{Resource: "user", Verb: "update"},
		func(ctx context.Context, in *principalIDInput) (*respx.Body[PrincipalView], error) {
			tenantID, id, err := principalRef(in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err != nil {
				return nil, identityError(err)
			}
			principal, err := service.SetStatus(ctx, tenantID, id, "disabled")
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, profileView(principal, fields)), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-delete-principal-suspension",
		Method:      http.MethodDelete,
		Path:        "/principals/{principal_id}/suspension",
		Summary:     "Lift a principal suspension",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, authz.Guard{Resource: "user", Verb: "update"},
		func(ctx context.Context, in *principalIDInput) (*respx.Body[PrincipalView], error) {
			tenantID, id, err := principalRef(in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			fields, err := service.profileConstraint(ctx, tenantID)
			if err != nil {
				return nil, identityError(err)
			}
			principal, err := service.SetStatus(ctx, tenantID, id, "active")
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, profileView(principal, fields)), nil
		})
}

func registerServiceAccountHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "identity-list-service-accounts",
		Method:      http.MethodGet,
		Path:        "/service-accounts",
		Summary:     "List tenant service accounts",
		Tags:        []string{"directory"},
	}, authz.Guard{Resource: "service_account", Verb: "view"},
		func(ctx context.Context, in *serviceAccountListInput) (*respx.Body[serviceAccountListData], error) {
			tenantID, err := requireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			items, total, err := service.ListServiceAccounts(ctx, tenantID, in.Search, in.Limit, in.Offset)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, serviceAccountListData{Items: items, Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID:   "identity-create-service-account",
		Method:        http.MethodPost,
		Path:          "/service-accounts",
		Summary:       "Create a non-interactive tenant service account",
		Tags:          []string{"directory"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "service_account", Verb: "create"},
		func(ctx context.Context, in *serviceAccountCreateInput) (*respx.Body[ServiceAccount], error) {
			tenantID, err := requireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			account, err := service.CreateServiceAccount(
				ctx, tenantID, in.Body.LoginName, in.Body.DisplayName, in.Body.Description, in.Body.ExpiresAt,
			)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, account), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "identity-get-service-account",
		Method:      http.MethodGet,
		Path:        "/service-accounts/{service_account_id}",
		Summary:     "Get a tenant service account",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "view"},
		func(ctx context.Context, in *serviceAccountIDInput) (*respx.Body[ServiceAccount], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			account, err := service.GetServiceAccount(ctx, tenantID, id)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, account), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "identity-replace-service-account",
		Method:      http.MethodPut,
		Path:        "/service-accounts/{service_account_id}",
		Summary:     "Replace service account profile and status",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "update"},
		func(ctx context.Context, in *serviceAccountReplaceInput) (*respx.Body[ServiceAccount], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			account, err := service.ReplaceServiceAccount(
				ctx, tenantID, id, in.Body.DisplayName, in.Body.Description,
				in.Body.Status, in.Body.ExpiresAt, in.Body.Version,
			)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, account), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "identity-delete-service-account",
		Method:      http.MethodDelete,
		Path:        "/service-accounts/{service_account_id}",
		Summary:     "Delete a service account and revoke its credentials",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound, http.StatusConflict},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "delete"},
		func(ctx context.Context, in *serviceAccountDeleteInput) (*respx.Body[serviceAccountDeleteData], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			if err := service.DeleteServiceAccount(ctx, tenantID, id, in.Version); err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, serviceAccountDeleteData{Deleted: true}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "identity-list-service-account-credentials",
		Method:      http.MethodGet,
		Path:        "/service-accounts/{service_account_id}/credentials",
		Summary:     "List service account credentials without secrets",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "manage_credential"},
		func(ctx context.Context, in *serviceAccountIDInput) (*respx.Body[[]ServiceAccountCredential], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			credentials, err := service.ListServiceAccountCredentials(ctx, tenantID, id)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, credentials), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID:   "identity-create-service-account-credential",
		Method:        http.MethodPost,
		Path:          "/service-accounts/{service_account_id}/credentials",
		Summary:       "Create a service account client credential",
		Tags:          []string{"directory"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "manage_credential"},
		func(ctx context.Context, in *serviceAccountCredentialCreateInput) (*respx.Body[ServiceAccountCredentialSecret], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			credential, err := service.CreateServiceAccountCredential(
				ctx, tenantID, id, in.Body.Name, in.Body.Scopes, in.Body.ExpiresAt,
			)
			if err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, credential), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "identity-delete-service-account-credential",
		Method:      http.MethodDelete,
		Path:        "/service-accounts/{service_account_id}/credentials/{credential_id}",
		Summary:     "Revoke a service account credential and its active tokens",
		Tags:        []string{"directory"},
		Errors:      []int{http.StatusNotFound},
	}, "service_account_id", service.resolveServiceAccountOwner, authz.Guard{Resource: "service_account", Verb: "manage_credential"},
		func(ctx context.Context, in *serviceAccountCredentialIDInput) (*respx.Body[serviceAccountCredentialRevokeData], error) {
			id, err := requireID(in.ID)
			if err != nil {
				return nil, err
			}
			tenantID := authnext.TenantIDFromContext(ctx)
			credentialID, err := requireID(in.CredentialID)
			if err != nil {
				return nil, err
			}
			if err := service.RevokeServiceAccountCredential(ctx, tenantID, id, credentialID); err != nil {
				return nil, identityError(err)
			}
			return respx.OK(ctx, serviceAccountCredentialRevokeData{Revoked: true}), nil
		})
}

// serviceAccountDeleteData and serviceAccountCredentialRevokeData replace the
// source's untyped map bodies so the OpenAPI document describes the response.
type serviceAccountDeleteData struct {
	Deleted bool `json:"deleted"`
}

type serviceAccountCredentialRevokeData struct {
	Revoked bool `json:"revoked"`
}

// identityError maps one domain error to one transport status and message key.
// Principal and service-account failures share a mapper because both surfaces
// report the same tenant, conflict, and validation semantics.
func identityError(err error) error {
	// The lifecycle refusals are checked first: several of them wrap conditions
	// the general cases below would flatten into a less useful status.
	if mapped, handled := lifecycleAPIError(err); handled {
		return mapped
	}
	switch {
	case errors.Is(err, authz.ErrFieldDenied):
		return huma.Error403Forbidden("respx.forbidden")
	case errors.Is(err, authz.ErrFieldsUnavailable):
		return huma.Error503ServiceUnavailable(identityi18n.IdentityUnavailable)
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(identityi18n.PrincipalNotFound)
	case errors.Is(err, ErrServiceAccountNotFound):
		return huma.Error404NotFound(identityi18n.ServiceAccountNotFound)
	case errors.Is(err, ErrCredentialNotFound):
		return huma.Error404NotFound(identityi18n.ServiceAccountCredentialNotFound)
	case errors.Is(err, ErrLoginConflict):
		return huma.Error409Conflict(identityi18n.LoginNameExists)
	case errors.Is(err, ErrServiceAccountConflict):
		return huma.Error409Conflict(identityi18n.ServiceAccountVersionConflict)
	case errors.Is(err, ErrCredentialLimit):
		return huma.Error409Conflict(identityi18n.ServiceAccountCredentialLimit)
	case errors.Is(err, core.ErrLastTenantAdministrator):
		return huma.Error409Conflict(identityi18n.LastTenantAdministrator)
	case errors.Is(err, ErrPrincipalInactive):
		return huma.Error409Conflict(identityi18n.InvalidPrincipal)
	case errors.Is(err, ErrServiceAccountInvalid):
		return huma.Error422UnprocessableEntity(identityi18n.InvalidServiceAccount)
	case errors.Is(err, ErrInvalid):
		return huma.Error422UnprocessableEntity(identityi18n.InvalidPrincipal)
	default:
		return huma.Error500InternalServerError(identityi18n.IdentityUnavailable)
	}
}
