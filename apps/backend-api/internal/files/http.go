package files

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	filesi18n "7link.com/platform/internal/files/i18n"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

// FilePageInput is exported so Huma discovers its embedded query parameters.
type FilePageInput struct {
	After string `query:"after" doc:"exclusive snowflake cursor returned by the previous page"`
	Limit int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	State string `query:"state" enum:"active,trashed" default:"active" doc:"active roots or authorized recycle-bin entries"`
}

type tenantFilePageInput struct {
	FilePageInput
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type entityFilePageInput struct {
	FilePageInput
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type departmentFilePageInput struct {
	FilePageInput
	DepartmentID string `path:"department_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type fileItemsInput struct {
	After  string `query:"after" doc:"exclusive snowflake cursor returned by the previous page"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	FileID string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"globally unique directory file identifier"`
}

type fileResourceInput struct {
	FileID      string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"globally unique file identifier"`
	Disposition string `query:"disposition" enum:"attachment,inline" default:"attachment" doc:"browser content disposition"`
}

type filePathInput struct {
	RootFileID string `path:"root_file_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"globally unique authorized root file identifier"`
	Path       string `path:"*" doc:"slash-separated path relative to the selected root; spans multiple URL segments"`
}

type fileCreateForm struct {
	ParentID string        `form:"parent_id" required:"false"`
	Kind     string        `form:"kind" enum:"file,directory" required:"true"`
	Name     string        `form:"name" maxLength:"255" required:"false"`
	Scope    string        `form:"scope" enum:"account,system" required:"false"`
	File     huma.FormFile `form:"file" contentType:"application/octet-stream" required:"false"`
}

type fileCreateInput struct {
	RawBody huma.MultipartFormFiles[fileCreateForm]
}

type tenantFileCreateInput struct {
	RawBody  huma.MultipartFormFiles[fileCreateForm]
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type entityFileCreateInput struct {
	RawBody  huma.MultipartFormFiles[fileCreateForm]
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type departmentFileCreateInput struct {
	RawBody      huma.MultipartFormFiles[fileCreateForm]
	DepartmentID string `path:"department_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type fileUpdateBody struct {
	Name     *string `json:"name,omitempty" minLength:"1" maxLength:"255"`
	State    *string `json:"state,omitempty" enum:"active"`
	Revision int64   `json:"revision" minimum:"1"`
}

type fileUpdateInput struct {
	FileID string         `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"file identifier"`
	Body   fileUpdateBody `json:"body"`
}

type fileDeleteInput struct {
	FileID   string `path:"file_id" pattern:"^[1-9][0-9]*$" maxLength:"19" doc:"file identifier"`
	Revision int64  `query:"revision" minimum:"1"`
}

type fileOperationDecisionInput struct {
	ConflictKey string `json:"conflict_key" minLength:"64" maxLength:"64"`
	Policy      string `json:"policy" enum:"overwrite,merge,replace,skip,rename"`
}

type fileOperationInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"128" doc:"stable key for one actor, space, and canonical request"`
	Body           struct {
		Action           string                       `json:"action" enum:"copy,move"`
		DestinationID    string                       `json:"destination_id"`
		Sources          []string                     `json:"sources" minItems:"1" maxItems:"1000"`
		FilePolicy       string                       `json:"file_policy" enum:"ask,overwrite,skip,rename"`
		DirectoryPolicy  string                       `json:"directory_policy" enum:"ask,merge,replace,skip,rename"`
		TypePolicy       string                       `json:"type_policy" enum:"ask,replace,skip,rename"`
		ObservedRevision int64                        `json:"observed_revision,omitempty" minimum:"0"`
		Decisions        []fileOperationDecisionInput `json:"decisions,omitempty" maxItems:"1000"`
	}
}

type fileConflictHTTPError struct {
	status int

	Code    int                 `json:"code"`
	Message string              `json:"message"`
	Meta    respx.Meta          `json:"meta"`
	Data    *immediateConflicts `json:"data,omitempty"`
}

func (e *fileConflictHTTPError) Error() string  { return e.Message }
func (e *fileConflictHTTPError) GetStatus() int { return e.status }

func registerFileHTTP(api huma.API, service *operationService, registrar *authz.Registrar, fileOwner, entityOwner, departmentOwner authz.ResourceOwnerResolver) {
	registerSystemFileHTTP(api, service, registrar)
	authz.RegisterPrincipalAuthenticated(registrar, api, huma.Operation{
		OperationID: "list-my-files", Method: http.MethodGet, Path: "/principals/{principal_id}/files",
		Summary: "List my file roots", Tags: []string{"files"}, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, input *FilePageInput) (*respx.Body[filePage], error) {
		ctx = personalContext(ctx)
		claims, _ := authn.FromContext(ctx)
		return listFileCollection(ctx, service, fileCollection{scope: ownerAccount, ownerID: claims.PrincipalID}, *input)
	})
	authz.Register(registrar, api, huma.Operation{
		OperationID: "list-tenant-files", Method: http.MethodGet, Path: "/files",
		Summary: "List tenant file roots", Tags: []string{"files"}, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *tenantFilePageInput) (*respx.Body[filePage], error) {
		claims, _ := authn.FromContext(ctx)
		return listFileCollection(ctx, service, fileCollection{scope: ownerTenant, tenantID: claims.TenantID, ownerID: claims.TenantID}, input.FilePageInput)
	})
	authz.RegisterOwnedTenantMember(registrar, api, huma.Operation{
		OperationID: "list-entity-files", Method: http.MethodGet, Path: "/entities/{entity_id}/files",
		Summary: "List entity file roots", Tags: []string{"files"}, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, "entity_id", entityOwner, func(ctx context.Context, input *entityFilePageInput) (*respx.Body[filePage], error) {
		claims, _ := authn.FromContext(ctx)
		return listFileCollection(ctx, service, fileCollection{scope: ownerEntity, tenantID: claims.TenantID, ownerID: claims.EntityID}, input.FilePageInput)
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "list-department-files", Method: http.MethodGet, Path: "/departments/{department_id}/files",
		Summary: "List department file roots", Tags: []string{"files"}, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, "department_id", departmentOwner, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *departmentFilePageInput) (*respx.Body[filePage], error) {
		claims, _ := authn.FromContext(ctx)
		departmentID, err := guid.RequireID(input.DepartmentID)
		if err != nil {
			return nil, err
		}
		return listFileCollection(ctx, service, fileCollection{scope: ownerDepartment, tenantID: claims.TenantID, ownerID: departmentID}, input.FilePageInput)
	})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "list-file-items", Method: http.MethodGet,
		Path:    "/files/{file_id}/items",
		Summary: "List an authorized directory page", Tags: []string{"files"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "file_id", fileOwner, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileItemsInput) (*respx.Body[fileChildrenPage], error) {
		parentID, err := guid.RequireID(input.FileID)
		if err != nil {
			return nil, err
		}
		page, err := browserPageOf(input.After, input.Limit)
		if err != nil {
			return nil, err
		}
		result, err := service.listChildren(ctx, parentID, page)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterPrincipalAuthenticated(registrar, api, huma.Operation{
		OperationID: "create-my-file", Method: http.MethodPost, Path: "/principals/{principal_id}/files",
		Summary: "Create a directory or upload a file", Tags: []string{"files"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20),
		Errors:       []int{http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, input *fileCreateInput) (*respx.Body[fileEntryView], error) {
		return createFile(personalContext(ctx), service, input.RawBody.Data(), true)
	})
	authz.Register(registrar, api, huma.Operation{
		OperationID: "create-tenant-file", Method: http.MethodPost, Path: "/files",
		Summary: "Create a tenant file", Tags: []string{"files"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20), Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "create", FilterData: true}, func(ctx context.Context, input *tenantFileCreateInput) (*respx.Body[fileEntryView], error) {
		return createFile(ctx, service, input.RawBody.Data(), false)
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "create-entity-file", Method: http.MethodPost, Path: "/entities/{entity_id}/files",
		Summary: "Create an entity file", Tags: []string{"files"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20), Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, "entity_id", entityOwner, authz.Guard{Resource: "file", Verb: "create", FilterData: true}, func(ctx context.Context, input *entityFileCreateInput) (*respx.Body[fileEntryView], error) {
		return createFile(ctx, service, input.RawBody.Data(), false)
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "create-department-file", Method: http.MethodPost, Path: "/departments/{department_id}/files",
		Summary: "Create a department file", Tags: []string{"files"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20), Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, "department_id", departmentOwner, authz.Guard{Resource: "file", Verb: "create", FilterData: true}, func(ctx context.Context, input *departmentFileCreateInput) (*respx.Body[fileEntryView], error) {
		return createFile(ctx, service, input.RawBody.Data(), false)
	})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "get-file", Method: http.MethodGet, Path: "/files/{file_id}",
		Summary: "Get authorized file metadata", Tags: []string{"files"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "file_id", fileOwner, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileResourceInput) (*respx.Body[fileEntryView], error) {
		fileID, err := guid.RequireID(input.FileID)
		if err != nil {
			return nil, err
		}
		result, err := service.getFileEntry(ctx, fileID)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	const runtimeFilePath = "/files/{root_file_id}/-/*"
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "get-file-by-path", Method: http.MethodGet, Path: runtimeFilePath,
		Summary: "Get authorized file metadata by a root-relative path", Tags: []string{"files"},
		Description: "The path spans every remaining URL segment: /files/{root_file_id}/-/a/b/c.txt names c.txt " +
			"inside a/b under the root. OpenAPI has no multi-segment parameter, so the value is published as {path}; " +
			"a client sends the segments separated by / and escapes each segment on its own. An escaped separator " +
			"(%2F) is decoded before the lookup and names the same file as /. Empty, leading or trailing segments " +
			"and backslashes are rejected with 422.",
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "root_file_id", fileOwner, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *filePathInput) (*respx.Body[fileEntryView], error) {
		rootID, err := guid.RequireID(input.RootFileID)
		if err != nil {
			return nil, err
		}
		result, err := service.getFileEntryByPath(ctx, rootID, input.Path)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})
	documentFilePath(api, runtimeFilePath, "/files/{root_file_id}/-/{path}")

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "download-file", Method: http.MethodGet, Path: "/files/{file_id}/content",
		Summary: "Download file content", Tags: []string{"files"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "file_id", fileOwner, authz.Guard{Resource: "file", Verb: "download", FilterData: true}, func(ctx context.Context, input *fileResourceInput) (*huma.StreamResponse, error) {
		fileID, err := guid.RequireID(input.FileID)
		if err != nil {
			return nil, err
		}
		result, err := service.downloadFile(ctx, fileID)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		disposition := input.Disposition
		if disposition == "" {
			disposition = "attachment"
		}
		return &huma.StreamResponse{Body: func(stream huma.Context) {
			defer func() { _ = result.Reader.Close() }()
			stream.SetHeader("Content-Type", result.MIME)
			stream.SetHeader("Content-Length", strconv.FormatInt(result.Size, 10))
			stream.SetHeader("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": result.Name}))
			stream.SetHeader("X-Content-Type-Options", "nosniff")
			_, _ = io.Copy(stream.BodyWriter(), result.Reader)
		}}, nil
	})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "update-file", Method: http.MethodPatch, Path: "/files/{file_id}",
		Summary: "Update a file or directory", Tags: []string{"files"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "file_id", fileOwner, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileUpdateInput) (*respx.Body[fileEntryView], error) {
		fileID, err := guid.RequireID(input.FileID)
		if err != nil {
			return nil, err
		}
		var result fileEntryView
		switch {
		case input.Body.Name != nil && input.Body.State == nil:
			result, err = service.renameNode(ctx, fileID, input.Body.Revision, *input.Body.Name)
		case input.Body.State != nil && *input.Body.State == "active":
			name := ""
			if input.Body.Name != nil {
				name = *input.Body.Name
			}
			result, err = service.restoreNodeAs(ctx, fileID, input.Body.Revision, name)
		default:
			return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileUpdate)
		}
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "delete-file", Method: http.MethodDelete, Path: "/files/{file_id}",
		Summary: "Move a file or directory to trash", Tags: []string{"files"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "file_id", fileOwner, authz.Guard{Resource: "file", Verb: "delete", FilterData: true}, func(ctx context.Context, input *fileDeleteInput) (*respx.Body[fileTrashResult], error) {
		fileID, err := guid.RequireID(input.FileID)
		if err != nil {
			return nil, err
		}
		result, err := service.trashNode(ctx, fileID, input.Revision)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "create-file-operation", Method: http.MethodPost, Path: "/file-operations",
		Summary: "Copy or move files synchronously", Tags: []string{"files"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, authz.Guard{Resource: "file", Verb: "create", FilterData: true}, func(ctx context.Context, input *fileOperationInput) (*respx.Body[operationResult], error) {
		request, err := fileOperationRequestOf(input)
		if err != nil {
			return nil, err
		}
		result, err := service.run(ctx, input.IdempotencyKey, request)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

}

func listFileCollection(ctx context.Context, service *operationService, collection fileCollection, input FilePageInput) (*respx.Body[filePage], error) {
	page, err := browserPageOf(input.After, input.Limit)
	if err != nil {
		return nil, err
	}
	var result filePage
	if input.State == "trashed" {
		result, err = service.listTrash(ctx, collection, page)
	} else {
		result, err = service.listRoots(ctx, collection, page)
	}
	if err != nil {
		return nil, fileHTTPError(err)
	}
	return respx.OK(ctx, result), nil
}

// personalContext drops the selected tenant from a request about the caller's
// own files.
//
// A person's file space belongs to no tenant, so which tenant the session
// currently has selected is not part of the question. The registrar already
// answers it this way: when it resolves a personally owned resource it clears
// the tenant and entity before the handler runs
// (ownedResourceMiddleware). Principal file collections have no file resource in the
// path to resolve, so they say the same thing here instead of inheriting a
// tenant that made listing the caller's own roots fail.
func personalContext(ctx context.Context) context.Context {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.TenantID == 0 && claims.EntityID == 0 {
		return ctx
	}
	personal := *claims
	personal.TenantID, personal.EntityID = 0, 0
	return authn.WithClaims(ctx, &personal)
}

func createFile(ctx context.Context, service *operationService, form *fileCreateForm, allowRoot bool) (*respx.Body[fileEntryView], error) {
	if form == nil {
		return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileContent)
	}
	var result fileEntryView
	var err error
	if strings.TrimSpace(form.ParentID) == "" {
		if !allowRoot || form.Kind != "directory" || form.Scope != ownerAccount || strings.TrimSpace(form.Name) != "" || form.File.IsSet {
			return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileRoot)
		}
		result, err = service.ensureOwnedRoot(ctx)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	}
	if form.Scope != "" {
		return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileScope)
	}
	parentID, err := guid.RequireID(form.ParentID)
	if err != nil {
		return nil, err
	}
	switch form.Kind {
	case "directory":
		if form.File.IsSet {
			return nil, huma.Error422UnprocessableEntity(filesi18n.DirectoryContentNotAllowed)
		}
		result, err = service.createDirectory(ctx, parentID, form.Name)
	case "file":
		if !form.File.IsSet || strings.TrimSpace(form.Name) != "" {
			return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileContent)
		}
		result, err = service.uploadFile(ctx, parentID, form.File.Filename, form.File.ContentType, form.File, form.File.Size)
	default:
		return nil, huma.Error422UnprocessableEntity(filesi18n.InvalidFileKind)
	}
	if err != nil {
		return nil, fileHTTPError(err)
	}
	return respx.OK(ctx, result), nil
}

func documentFilePath(api huma.API, runtimePath, documentedPath string) {
	// Chi uses a trailing catch-all for real multi-segment paths. OpenAPI has no
	// catch-all syntax, so publish the same value as a conventional path parameter.
	item := api.OpenAPI().Paths[runtimePath]
	if item == nil || item.Get == nil {
		panic("file path operation was not registered")
	}
	documentedItem := *item
	documentedOperation := *item.Get
	documentedOperation.Parameters = make([]*huma.Param, len(item.Get.Parameters))
	for index, parameter := range item.Get.Parameters {
		clonedParameter := *parameter
		if clonedParameter.In == "path" && clonedParameter.Name == "*" {
			clonedParameter.Name = "path"
		}
		documentedOperation.Parameters[index] = &clonedParameter
	}
	documentedItem.Get = &documentedOperation
	delete(api.OpenAPI().Paths, runtimePath)
	api.OpenAPI().Paths[documentedPath] = &documentedItem
}

func browserPageOf(after string, limit int) (browserPageRequest, error) {
	var cursor guid.ID
	if strings.TrimSpace(after) != "" {
		var err error
		cursor, err = guid.RequireID(after)
		if err != nil {
			return browserPageRequest{}, err
		}
	}
	request, err := normalizeBrowserPage(browserPageRequest{After: cursor, Limit: limit})
	if err != nil {
		return browserPageRequest{}, fileHTTPError(err)
	}
	return request, nil
}

func fileOperationRequestOf(input *fileOperationInput) (immediateOperationRequest, error) {
	if input == nil {
		return immediateOperationRequest{}, huma.Error422UnprocessableEntity(filesi18n.InvalidFileOperation)
	}
	destinationID, err := guid.RequireID(input.Body.DestinationID)
	if err != nil {
		return immediateOperationRequest{}, err
	}
	sources := make([]guid.ID, len(input.Body.Sources))
	for index, raw := range input.Body.Sources {
		sources[index], err = guid.RequireID(raw)
		if err != nil {
			return immediateOperationRequest{}, err
		}
	}
	decisions := make([]immediateDecision, len(input.Body.Decisions))
	for index, decision := range input.Body.Decisions {
		decisions[index] = immediateDecision(decision)
	}
	request := immediateOperationRequest{
		operationRequest: operationRequest{
			// The service derives SpaceID from the authorized destination. It is
			// deliberately absent from the public contract.
			SpaceID: 1, Action: input.Body.Action, DestinationID: destinationID, Sources: sources,
			FilePolicy: input.Body.FilePolicy, DirectoryPolicy: input.Body.DirectoryPolicy, TypePolicy: input.Body.TypePolicy,
		},
		ObservedRevision: input.Body.ObservedRevision, Decisions: decisions,
	}
	request, _, _, err = canonicalImmediateRequest(request, input.IdempotencyKey, operationBudget{Nodes: 1000, ClosureRows: 100000})
	if err != nil {
		return immediateOperationRequest{}, fileHTTPError(err)
	}
	request.SpaceID = 0
	return request, nil
}

func fileHTTPError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	var conflicts *immediateConflicts
	switch {
	case errors.As(err, &conflicts):
		return &fileConflictHTTPError{
			status: http.StatusConflict, Code: http.StatusConflict, Message: "file_operation_conflict",
			Meta: respx.Meta{RequestAt: time.Now().UTC()}, Data: conflicts,
		}
	case errors.Is(err, errOperationForbidden):
		return huma.Error403Forbidden(respx.Forbidden)
	case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return huma.Error404NotFound(respx.NotFound)
	case errors.Is(err, errRestoreNameConflict):
		return huma.Error409Conflict("file_restore_name_conflict")
	case errors.Is(err, errTreeConflict), errors.Is(err, errVersionConflict), errors.Is(err, errOperationKey), errors.Is(err, errOperationState), errors.Is(err, core.ErrAuthorizationChanged):
		return huma.Error409Conflict("file_state_changed")
	case errors.Is(err, errOperationInput), errors.Is(err, errFileACL), errors.Is(err, errHierarchy), errors.Is(err, errRootUnbound),
		errors.Is(err, ErrName), errors.Is(err, ErrLength), errors.Is(err, ErrInvalidKey):
		return huma.Error422UnprocessableEntity(filesi18n.InvalidFileRequest)
	case errors.Is(err, errOperationUnavailable), errors.Is(err, context.DeadlineExceeded):
		return huma.Error503ServiceUnavailable(filesi18n.FileServiceUnavailable)
	default:
		return err
	}
}
