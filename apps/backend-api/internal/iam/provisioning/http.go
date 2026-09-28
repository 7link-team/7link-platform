package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	provisioningi18n "7link.com/platform/internal/iam/provisioning/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	registerAdminHTTP(api, service, registrar)
	registerTargetHTTP(api, service, registrar)
	registerSCIMHTTP(api, service)
}

func parseSCIMID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil {
		return 0, ErrInvalidSCIM
	}
	return id, nil
}

// directoryListInput bounds the listing by default: a tenant's directory count
// is open-ended, and an omitted limit used to mean "every row".
type directoryListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// directoryIDInput addresses one directory's credential listing, which the
// directory accumulates over its lifetime, so it is bounded by default too.
type directoryIDInput struct {
	DirectoryID string `path:"directory_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Limit       int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type directoryCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Name             string   `json:"name" minLength:"1" maxLength:"128"`
		ServiceAccountID *guid.ID `json:"service_account_id,omitempty" nullable:"true"`
	}
}

type directoryReplaceInput struct {
	DirectoryID string `path:"directory_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		Name             string   `json:"name" minLength:"1" maxLength:"128"`
		Status           string   `json:"status" enum:"active,disabled"`
		Version          int64    `json:"version" minimum:"1"`
		ServiceAccountID *guid.ID `json:"service_account_id,omitempty" nullable:"true"`
	}
}

type credentialCreateInput struct {
	DirectoryID string `path:"directory_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		Name      string     `json:"name" minLength:"1" maxLength:"128"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
}

type credentialIDInput struct {
	DirectoryID  string `path:"directory_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	CredentialID string `path:"credential_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func registerAdminHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	guard := authz.Guard{Resource: "tenant", Verb: "administer"}
	authz.Register(registrar, api, huma.Operation{OperationID: "provisioning-list-scim-directories", Method: http.MethodGet, Path: "/scim-directories", Summary: "List tenant SCIM directories", Tags: []string{"applications"}}, guard, func(ctx context.Context, in *directoryListInput) (*respx.Body[[]Directory], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListDirectoriesPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})
	authz.Register(registrar, api, huma.Operation{OperationID: "provisioning-create-scim-directory", Method: http.MethodPost, Path: "/scim-directories", Summary: "Create a tenant SCIM directory", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}, guard, func(ctx context.Context, in *directoryCreateInput) (*respx.Body[Directory], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateDirectory(ctx, tenantID, in.Body.Name, in.Body.ServiceAccountID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-replace-scim-directory", Method: http.MethodPut, Path: "/scim-directories/{directory_id}", Summary: "Replace a tenant SCIM directory", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "directory_id", service.resolveDirectoryOwner, guard, func(ctx context.Context, in *directoryReplaceInput) (*respx.Body[Directory], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		directoryID, err := guid.RequireID(in.DirectoryID)
		if err != nil {
			return nil, err
		}
		item, err := service.ReplaceDirectory(ctx, tenantID, directoryID, in.Body.Name, in.Body.Status, in.Body.Version, in.Body.ServiceAccountID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-list-scim-credentials", Method: http.MethodGet, Path: "/scim-directories/{directory_id}/credentials", Summary: "List SCIM directory credentials", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound}}, "directory_id", service.resolveDirectoryOwner, guard, func(ctx context.Context, in *directoryIDInput) (*respx.Body[[]Credential], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		directoryID, err := guid.RequireID(in.DirectoryID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListCredentialsPage(ctx, tenantID, directoryID, in.Offset, in.Limit)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-create-scim-credential", Method: http.MethodPost, Path: "/scim-directories/{directory_id}/credentials", Summary: "Create a one-display SCIM bearer credential", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "directory_id", service.resolveDirectoryOwner, guard, func(ctx context.Context, in *credentialCreateInput) (*respx.Body[CredentialSecret], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		directoryID, err := guid.RequireID(in.DirectoryID)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateCredential(ctx, tenantID, directoryID, in.Body.Name, in.Body.ExpiresAt)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-revoke-scim-credential", Method: http.MethodDelete, Path: "/scim-directories/{directory_id}/credentials/{credential_id}", Summary: "Revoke a SCIM bearer credential", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity}}, "directory_id", service.resolveDirectoryOwner, guard, func(ctx context.Context, in *credentialIDInput) (*respx.Body[map[string]bool], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		directoryID, err := guid.RequireID(in.DirectoryID)
		if err != nil {
			return nil, err
		}
		credentialID, err := guid.RequireID(in.CredentialID)
		if err != nil {
			return nil, err
		}
		if err := service.RevokeCredential(ctx, tenantID, directoryID, credentialID); err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, map[string]bool{"revoked": true}), nil
	})
}

func provisioningError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, authz.ErrFieldDenied) {
		return huma.Error403Forbidden(respx.Forbidden)
	}
	if errors.Is(err, authz.ErrFieldsUnavailable) {
		return huma.Error503ServiceUnavailable(provisioningi18n.Unavailable)
	}
	switch {
	case errors.Is(err, ErrInvalidDirectory):
		return huma.Error422UnprocessableEntity(provisioningi18n.InvalidDirectory)
	case errors.Is(err, ErrDirectoryMissing):
		return huma.Error404NotFound(provisioningi18n.DirectoryNotFound)
	case errors.Is(err, ErrDirectoryName):
		return huma.Error409Conflict(provisioningi18n.DirectoryNameExists)
	case errors.Is(err, ErrDirectoryVersion):
		return huma.Error409Conflict(provisioningi18n.DirectoryVersion)
	case errors.Is(err, ErrCredentialMissing):
		return huma.Error404NotFound(provisioningi18n.CredentialNotFound)
	case errors.Is(err, ErrCredentialLimit):
		return huma.Error409Conflict(provisioningi18n.CredentialLimit)
	case errors.Is(err, ErrInvalidTarget):
		return huma.Error422UnprocessableEntity(provisioningi18n.InvalidTarget)
	case errors.Is(err, ErrTargetMissing):
		return huma.Error404NotFound(provisioningi18n.TargetNotFound)
	case errors.Is(err, ErrTargetName):
		return huma.Error409Conflict(provisioningi18n.TargetNameExists)
	case errors.Is(err, ErrTargetVersion):
		return huma.Error409Conflict(provisioningi18n.TargetVersion)
	case errors.Is(err, ErrTargetBusy):
		return huma.Error409Conflict(provisioningi18n.TargetBusy)
	case errors.Is(err, ErrTargetRetiring):
		return huma.Error409Conflict(provisioningi18n.TargetRetiring)
	case errors.Is(err, ErrTargetKeyMissing):
		return huma.Error422UnprocessableEntity(provisioningi18n.TargetKeyMissing)
	case errors.Is(err, ErrTargetDisabled):
		return huma.Error409Conflict(provisioningi18n.TargetDisabled)
	case errors.Is(err, ErrTargetHasLiveMappings):
		return huma.Error409Conflict(provisioningi18n.TargetHasLiveMappings)
	case errors.Is(err, ErrUnauthorized):
		return huma.Error409Conflict(provisioningi18n.TargetServiceAccountInactive)
	case errors.Is(err, ErrDeprovisionMissing):
		return huma.Error404NotFound(provisioningi18n.TargetResourceNotMapped)
	case errors.Is(err, ErrResourceMissing):
		return huma.Error404NotFound(provisioningi18n.ResourceNotFound)
	case errors.Is(err, ErrRemoteUnavailable), errors.Is(err, ErrRemoteResponse):
		return huma.Error502BadGateway(provisioningi18n.TargetRemoteFailed)
	default:
		return huma.Error500InternalServerError(provisioningi18n.Unavailable)
	}
}

const scimBearerScheme = "scimBearer"

func registerSCIMHTTP(api huma.API, service *Service) {
	registerDiscovery(api)
	registerUserSCIM(api, service)
	registerGroupSCIM(api, service)
	registerSCIM(api, huma.Operation{OperationID: "scim-bulk", Method: http.MethodPost, Path: "/scim/v2/Bulk", Summary: "Execute SCIM bulk operations", Tags: []string{"scim"}}, reflect.TypeFor[BulkRequest](), reflect.TypeFor[BulkResponse](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		var input BulkRequest
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.Bulk(ctx.Context(), auth, input)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		writeSCIM(ctx, http.StatusOK, result)
	})
}

func registerUserSCIM(api huma.API, service *Service) {
	registerSCIM(api, huma.Operation{OperationID: "scim-list-users", Method: http.MethodGet, Path: "/scim/v2/Users", Summary: "List SCIM users", Tags: []string{"scim"}, Parameters: listParameters()}, nil, reflect.TypeFor[ListResponse[UserResource]](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		request, err := listRequest(ctx)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.ListUsers(ctx.Context(), auth, request)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, huma.Operation{OperationID: "scim-create-user", Method: http.MethodPost, Path: "/scim/v2/Users", Summary: "Create a SCIM user", Tags: []string{"scim"}, DefaultStatus: http.StatusCreated}, reflect.TypeFor[UserInput](), reflect.TypeFor[UserResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		var input UserInput
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.CreateUser(ctx.Context(), auth, input)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("Location", result.Meta.Location)
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusCreated, result)
	})
	registerUserItemSCIM(api, service)
}

func registerUserItemSCIM(api huma.API, service *Service) {
	path := "/scim/v2/Users/{id}"
	registerSCIM(api, itemOperation("scim-get-user", http.MethodGet, path, "Get a SCIM user"), nil, reflect.TypeFor[UserResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.GetUser(ctx.Context(), auth, id)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-replace-user", http.MethodPut, path, "Replace a SCIM user"), reflect.TypeFor[UserInput](), reflect.TypeFor[UserResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		var input UserInput
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.ReplaceUser(ctx.Context(), auth, id, input, version)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-patch-user", http.MethodPatch, path, "Patch a SCIM user"), reflect.TypeFor[PatchRequest](), reflect.TypeFor[UserResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		var input PatchRequest
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.PatchUser(ctx.Context(), auth, id, input, version)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-delete-user", http.MethodDelete, path, "Delete a SCIM user"), nil, nil, true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		id, idErr := parseSCIMID(ctx.Param("id"))
		if idErr != nil {
			writeSCIMError(ctx, idErr)
			return
		}
		if err == nil {
			err = service.DeleteUser(ctx.Context(), auth, id, version)
		}
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		writeSCIM(ctx, http.StatusNoContent, nil)
	})
}

func registerGroupSCIM(api huma.API, service *Service) {
	registerSCIM(api, huma.Operation{OperationID: "scim-list-groups", Method: http.MethodGet, Path: "/scim/v2/Groups", Summary: "List SCIM groups", Tags: []string{"scim"}, Parameters: listParameters()}, nil, reflect.TypeFor[ListResponse[GroupResource]](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		request, err := listRequest(ctx)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.ListGroups(ctx.Context(), auth, request)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, huma.Operation{OperationID: "scim-create-group", Method: http.MethodPost, Path: "/scim/v2/Groups", Summary: "Create a SCIM group", Tags: []string{"scim"}, DefaultStatus: http.StatusCreated}, reflect.TypeFor[GroupInput](), reflect.TypeFor[GroupResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		var input GroupInput
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.CreateGroup(ctx.Context(), auth, input)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("Location", result.Meta.Location)
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusCreated, result)
	})
	registerGroupItemSCIM(api, service)
}

func registerGroupItemSCIM(api huma.API, service *Service) {
	path := "/scim/v2/Groups/{id}"
	registerSCIM(api, itemOperation("scim-get-group", http.MethodGet, path, "Get a SCIM group"), nil, reflect.TypeFor[GroupResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.GetGroup(ctx.Context(), auth, id)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-replace-group", http.MethodPut, path, "Replace a SCIM group"), reflect.TypeFor[GroupInput](), reflect.TypeFor[GroupResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		var input GroupInput
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.ReplaceGroup(ctx.Context(), auth, id, input, version)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-patch-group", http.MethodPatch, path, "Patch a SCIM group"), reflect.TypeFor[PatchRequest](), reflect.TypeFor[GroupResource](), true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		var input PatchRequest
		if err := decodeSCIMBody(ctx, &input); err != nil {
			writeSCIMError(ctx, err)
			return
		}
		id, err := parseSCIMID(ctx.Param("id"))
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		result, err := service.PatchGroup(ctx.Context(), auth, id, input, version)
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		ctx.SetHeader("ETag", result.Meta.Version)
		writeSCIM(ctx, http.StatusOK, result)
	})
	registerSCIM(api, itemOperation("scim-delete-group", http.MethodDelete, path, "Delete a SCIM group"), nil, nil, true, func(ctx huma.Context) {
		auth, ok := authenticateSCIM(ctx, service)
		if !ok {
			return
		}
		version, err := parseETag(ctx.Header("If-Match"))
		id, idErr := parseSCIMID(ctx.Param("id"))
		if idErr != nil {
			writeSCIMError(ctx, idErr)
			return
		}
		if err == nil {
			err = service.DeleteGroup(ctx.Context(), auth, id, version)
		}
		if err != nil {
			writeSCIMError(ctx, err)
			return
		}
		writeSCIM(ctx, http.StatusNoContent, nil)
	})
}

func registerDiscovery(api huma.API) {
	registerSCIM(api, huma.Operation{OperationID: "scim-service-provider-config", Method: http.MethodGet, Path: "/scim/v2/ServiceProviderConfig", Summary: "Get SCIM service provider configuration", Tags: []string{"scim"}}, nil, reflect.TypeFor[ServiceProviderConfig](), false, func(ctx huma.Context) { writeSCIM(ctx, http.StatusOK, serviceProviderConfig()) })
	registerSCIM(api, huma.Operation{OperationID: "scim-list-schemas", Method: http.MethodGet, Path: "/scim/v2/Schemas", Summary: "List SCIM schemas", Tags: []string{"scim"}}, nil, reflect.TypeFor[ListResponse[SCIMSchemaResource]](), false, func(ctx huma.Context) {
		items := schemaResources()
		writeSCIM(ctx, http.StatusOK, ListResponse[SCIMSchemaResource]{Schemas: []string{ListSchema}, TotalResults: len(items), StartIndex: 1, ItemsPerPage: len(items), Resources: items})
	})
	registerSCIM(api, itemOperation("scim-get-schema", http.MethodGet, "/scim/v2/Schemas/{id}", "Get a SCIM schema"), nil, reflect.TypeFor[SCIMSchemaResource](), false, func(ctx huma.Context) {
		for _, item := range schemaResources() {
			if item.ID == ctx.Param("id") {
				writeSCIM(ctx, http.StatusOK, item)
				return
			}
		}
		writeSCIMError(ctx, ErrResourceMissing)
	})
	registerSCIM(api, huma.Operation{OperationID: "scim-list-resource-types", Method: http.MethodGet, Path: "/scim/v2/ResourceTypes", Summary: "List SCIM resource types", Tags: []string{"scim"}}, nil, reflect.TypeFor[ListResponse[ResourceTypeResource]](), false, func(ctx huma.Context) {
		items := resourceTypes()
		writeSCIM(ctx, http.StatusOK, ListResponse[ResourceTypeResource]{Schemas: []string{ListSchema}, TotalResults: len(items), StartIndex: 1, ItemsPerPage: len(items), Resources: items})
	})
	registerSCIM(api, itemOperation("scim-get-resource-type", http.MethodGet, "/scim/v2/ResourceTypes/{id}", "Get a SCIM resource type"), nil, reflect.TypeFor[ResourceTypeResource](), false, func(ctx huma.Context) {
		for _, item := range resourceTypes() {
			if item.ID == ctx.Param("id") {
				writeSCIM(ctx, http.StatusOK, item)
				return
			}
		}
		writeSCIMError(ctx, ErrResourceMissing)
	})
}

func registerSCIM(api huma.API, op huma.Operation, request, response reflect.Type, secured bool, handler func(huma.Context)) {
	authz.Public(&op)
	if secured {
		components := api.OpenAPI().Components
		if components.SecuritySchemes == nil {
			components.SecuritySchemes = map[string]*huma.SecurityScheme{}
		}
		components.SecuritySchemes[scimBearerScheme] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: "SCIM directory token"}
		op.Security = []map[string][]string{{scimBearerScheme: {}}}
	}
	if request != nil {
		op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{SCIMContentType: {Schema: huma.SchemaFromType(api.OpenAPI().Components.Schemas, request)}}}
	}
	op.Responses = map[string]*huma.Response{}
	if response == nil {
		op.Responses["204"] = &huma.Response{Description: "The operation completed successfully"}
	} else {
		status := "200"
		if op.DefaultStatus != 0 {
			status = strconv.Itoa(op.DefaultStatus)
		}
		op.Responses[status] = scimOpenAPIResponse("SCIM response", huma.SchemaFromType(api.OpenAPI().Components.Schemas, response))
	}
	errorSchema := huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeFor[SCIMError]())
	for _, status := range []string{"400", "401", "403", "404", "409", "412", "413", "500", "503"} {
		op.Responses[status] = scimOpenAPIResponse("SCIM error", errorSchema)
	}
	api.OpenAPI().AddOperation(&op)
	api.Adapter().Handle(&op, handler)
}

func scimOpenAPIResponse(description string, schema *huma.Schema) *huma.Response {
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{SCIMContentType: {Schema: schema}}}
}

func itemOperation(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"scim"}, Parameters: []*huma.Param{{Name: "id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(512)}}}}
}

func listParameters() []*huma.Param {
	return []*huma.Param{
		{Name: "filter", In: "query", Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(maxFilterBytes)}},
		{Name: "startIndex", In: "query", Schema: &huma.Schema{Type: huma.TypeInteger}},
		{Name: "count", In: "query", Schema: &huma.Schema{Type: huma.TypeInteger}},
	}
}

func authenticateSCIM(ctx huma.Context, service *Service) (AuthContext, bool) {
	if service == nil {
		return AuthContext{}, false
	}
	auth, err := service.Authenticate(ctx.Context(), ctx.Header("Authorization"))
	if err != nil {
		writeSCIMError(ctx, err)
		return AuthContext{}, false
	}
	return auth, true
}

func listRequest(ctx huma.Context) (ListRequest, error) {
	start, err := parseQueryInt(ctx.Query("startIndex"))
	if err != nil {
		return ListRequest{}, ErrInvalidSCIM
	}
	count, err := parseQueryInt(ctx.Query("count"))
	if err != nil {
		return ListRequest{}, ErrInvalidSCIM
	}
	requestURL := ctx.URL()
	if requestURL.Query().Has("count") && count == 0 {
		count = -1
	}
	return normalizeListRequest(ctx.Query("filter"), start, count)
}

func parseQueryInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}

func decodeSCIMBody(ctx huma.Context, target any) error {
	mediaType, _, err := mime.ParseMediaType(ctx.Header("Content-Type"))
	if err != nil || mediaType != SCIMContentType && mediaType != "application/json" {
		return ErrInvalidSCIM
	}
	data, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
	if err != nil {
		return ErrInvalidSCIM
	}
	if len(data) > maxBodyBytes {
		return ErrTooMany
	}
	return strictDecode(data, target)
}

func writeSCIMError(ctx huma.Context, err error) {
	protocolError := mapSCIMError(ctx.Context(), err)
	if protocolError.status >= http.StatusInternalServerError {
		slog.Error("SCIM request failed", "operation", ctx.Operation().OperationID, "err", err)
	}
	for name, values := range protocolError.headers {
		for _, value := range values {
			ctx.AppendHeader(name, value)
		}
	}
	writeSCIM(ctx, protocolError.status, protocolError)
}

func writeSCIM(ctx huma.Context, status int, value any) {
	ctx.SetHeader("Content-Type", SCIMContentType)
	ctx.SetStatus(status)
	if value == nil {
		return
	}
	if err := json.NewEncoder(ctx.BodyWriter()).Encode(value); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		slog.Error("write SCIM response", "operation", ctx.Operation().OperationID, "err", err)
	}
}

func intPointer(value int) *int { return &value }

// targetListInput bounds the listing by default, like directoryListInput.
type targetListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type targetIDInput struct {
	TargetID string `path:"target_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type targetCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Name             string   `json:"name" minLength:"1" maxLength:"128"`
		BaseURL          string   `json:"base_url" minLength:"1" maxLength:"1024"`
		BearerToken      string   `json:"bearer_token" minLength:"1" maxLength:"4096"`
		ServiceAccountID *guid.ID `json:"service_account_id,omitempty" nullable:"true"`
	}
}

type targetReplaceInput struct {
	TargetID string `path:"target_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Name             string   `json:"name" minLength:"1" maxLength:"128"`
		BaseURL          string   `json:"base_url" minLength:"1" maxLength:"1024"`
		Status           string   `json:"status" enum:"active,disabled"`
		Version          int64    `json:"version" minimum:"1"`
		BearerToken      string   `json:"bearer_token,omitempty" maxLength:"4096" doc:"Optional new bearer token; empty keeps the existing one."`
		ServiceAccountID *guid.ID `json:"service_account_id,omitempty" nullable:"true"`
	}
}

type targetPushInput struct {
	TargetID string `path:"target_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		ResourceType string `json:"resource_type" enum:"User,Group"`
		ResourceID   string `json:"resource_id" minLength:"1" maxLength:"128"`
	}
}

// registerTargetHTTP exposes the outbound SCIM target management API, guarded
// by the tenant administer permission like the rest of provisioning.
func registerTargetHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	guard := authz.Guard{Resource: "tenant", Verb: "administer"}
	authz.Register(registrar, api, huma.Operation{OperationID: "provisioning-list-scim-targets", Method: http.MethodGet, Path: "/scim-targets", Summary: "List outbound SCIM targets", Tags: []string{"applications"}}, guard, func(ctx context.Context, in *targetListInput) (*respx.Body[[]Target], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListTargetsPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})
	authz.Register(registrar, api, huma.Operation{OperationID: "provisioning-create-scim-target", Method: http.MethodPost, Path: "/scim-targets", Summary: "Create an outbound SCIM target", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}, guard, func(ctx context.Context, in *targetCreateInput) (*respx.Body[TargetSecret], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateTarget(ctx, tenantID, in.Body.Name, in.Body.BaseURL, in.Body.BearerToken, in.Body.ServiceAccountID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-replace-scim-target", Method: http.MethodPut, Path: "/scim-targets/{target_id}", Summary: "Replace an outbound SCIM target", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "target_id", service.resolveTargetOwner, guard, func(ctx context.Context, in *targetReplaceInput) (*respx.Body[Target], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		targetID, err := guid.RequireID(in.TargetID)
		if err != nil {
			return nil, err
		}
		item, err := service.ReplaceTarget(ctx, tenantID, targetID, in.Body.Name, in.Body.BaseURL, in.Body.Status, in.Body.BearerToken, in.Body.Version, in.Body.ServiceAccountID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-delete-scim-target", Method: http.MethodDelete, Path: "/scim-targets/{target_id}", Summary: "Delete an outbound SCIM target", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "target_id", service.resolveTargetOwner, guard, func(ctx context.Context, in *targetIDInput) (*respx.Body[map[string]bool], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		targetID, err := guid.RequireID(in.TargetID)
		if err != nil {
			return nil, err
		}
		if err := service.DeleteTarget(ctx, tenantID, targetID); err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, map[string]bool{"deleted": true}), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-retire-scim-target", Method: http.MethodPut, Path: "/scim-targets/{target_id}/retirement", Summary: "Create or resume retirement of an outbound SCIM target", Description: "Retirement blocks new provisioning and configuration changes, removes remote resources in pages, and deletes the target only once none remain. A definitive remote refusal preserves credentials and progress so another call can resume. A timeout, disconnect, asynchronous acceptance, or failed local completion leaves an unfinished operation that requires verified remote recovery; retrying never expires that protection.", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway}}, "target_id", service.resolveTargetOwner, guard, func(ctx context.Context, in *targetIDInput) (*respx.Body[map[string]int], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		targetID, err := guid.RequireID(in.TargetID)
		if err != nil {
			return nil, err
		}
		deprovisioned, err := service.RetireTarget(ctx, tenantID, targetID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, map[string]int{"deprovisioned": deprovisioned}), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-push-scim-resource", Method: http.MethodPost, Path: "/scim-targets/{target_id}/provisioning-jobs", Summary: "Create a user or group provisioning job", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway}}, "target_id", service.resolveTargetOwner, guard, func(ctx context.Context, in *targetPushInput) (*respx.Body[PushResult], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		targetID, err := guid.RequireID(in.TargetID)
		if err != nil {
			return nil, err
		}
		resourceID, err := guid.RequireID(in.Body.ResourceID)
		if err != nil {
			return nil, err
		}
		item, err := service.PushResource(ctx, tenantID, targetID, in.Body.ResourceType, resourceID)
		if err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "provisioning-deprovision-scim-resource", Method: http.MethodPost, Path: "/scim-targets/{target_id}/deprovisioning-jobs", Summary: "Create a user or group deprovisioning job", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway}}, "target_id", service.resolveTargetOwner, guard, func(ctx context.Context, in *targetPushInput) (*respx.Body[map[string]bool], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		targetID, err := guid.RequireID(in.TargetID)
		if err != nil {
			return nil, err
		}
		resourceID, err := guid.RequireID(in.Body.ResourceID)
		if err != nil {
			return nil, err
		}
		if err := service.DeprovisionResource(ctx, tenantID, targetID, in.Body.ResourceType, resourceID); err != nil {
			return nil, provisioningError(err)
		}
		return respx.OK(ctx, map[string]bool{"deprovisioned": true}), nil
	})
}
