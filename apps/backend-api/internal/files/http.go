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

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type filePageInput struct {
	After string `query:"after" doc:"exclusive snowflake cursor returned by the previous page"`
	Limit int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	State string `query:"state" enum:"active,trashed" default:"active" doc:"active roots or authorized recycle-bin entries"`
}

type fileItemsInput struct {
	After  string `query:"after" doc:"exclusive snowflake cursor returned by the previous page"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
	FileID string `path:"file_id" doc:"globally unique directory file identifier"`
}

type fileResourceInput struct {
	FileID      string `path:"file_id" doc:"globally unique file identifier"`
	Disposition string `query:"disposition" enum:"attachment,inline" default:"attachment" doc:"browser content disposition"`
}

type filePathInput struct {
	RootFileID string `path:"root_file_id" doc:"globally unique authorized root file identifier"`
	Path       string `path:"*" doc:"slash-separated path relative to the selected root"`
}

type fileCreateInput struct {
	RawBody huma.MultipartFormFiles[struct {
		ParentID string        `form:"parent_id" required:"false"`
		Kind     string        `form:"kind" enum:"file,directory" required:"true"`
		Name     string        `form:"name" maxLength:"255" required:"false"`
		Scope    string        `form:"scope" enum:"account,personal" required:"false"`
		File     huma.FormFile `form:"file" contentType:"application/octet-stream" required:"false"`
	}]
}

type fileUpdateBody struct {
	Name     *string `json:"name,omitempty" minLength:"1" maxLength:"255"`
	State    *string `json:"state,omitempty" enum:"active"`
	Revision int64   `json:"revision" minimum:"1"`
}

type fileUpdateInput struct {
	FileID string         `path:"file_id" doc:"file identifier"`
	Body   fileUpdateBody `json:"body"`
}

type fileDeleteInput struct {
	FileID   string `path:"file_id" doc:"file identifier"`
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

func registerFileHTTP(api huma.API, service *operationService, registrar *authz.Registrar) {
	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "list-files", Method: http.MethodGet, Path: "/files",
		Summary: "List authorized file roots", Tags: []string{"tenants"},
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *filePageInput) (*respx.Body[filePage], error) {
		page, err := browserPageOf(input.After, input.Limit)
		if err != nil {
			return nil, err
		}
		var result filePage
		if input.State == "trashed" {
			result, err = service.listTrash(ctx, page)
		} else {
			result, err = service.listRoots(ctx, page)
		}
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "list-file-items", Method: http.MethodGet,
		Path:    "/files/{file_id}/items",
		Summary: "List an authorized directory page", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileItemsInput) (*respx.Body[fileChildrenPage], error) {
		parentID, err := fileID(input.FileID)
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

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "create-file", Method: http.MethodPost, Path: "/files",
		Summary: "Create a directory or upload a file", Tags: []string{"tenants"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: service.maxUploadBytes + (1 << 20),
		Errors:       []int{http.StatusNotFound, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "create", FilterData: true}, func(ctx context.Context, input *fileCreateInput) (*respx.Body[fileEntryView], error) {
		form := input.RawBody.Data()
		var result fileEntryView
		var err error
		if strings.TrimSpace(form.ParentID) == "" {
			if form.Kind != "directory" || (form.Scope != "personal" && form.Scope != "account") || strings.TrimSpace(form.Name) != "" || form.File.IsSet {
				return nil, huma.Error422UnprocessableEntity("invalid_file_root")
			}
			result, err = service.ensureOwnedRoot(ctx, form.Scope)
			if err != nil {
				return nil, fileHTTPError(err)
			}
			return respx.OK(ctx, result), nil
		}
		if form.Scope != "" {
			return nil, huma.Error422UnprocessableEntity("invalid_file_scope")
		}
		parentID, err := fileID(form.ParentID)
		if err != nil {
			return nil, err
		}
		switch form.Kind {
		case "directory":
			if form.File.IsSet {
				return nil, huma.Error422UnprocessableEntity("directory_content_not_allowed")
			}
			result, err = service.createDirectory(ctx, parentID, form.Name)
		case "file":
			if !form.File.IsSet || strings.TrimSpace(form.Name) != "" {
				return nil, huma.Error422UnprocessableEntity("invalid_file_content")
			}
			result, err = service.uploadFile(ctx, parentID, form.File.Filename, form.File.ContentType, form.File, form.File.Size)
		default:
			return nil, huma.Error422UnprocessableEntity("invalid_file_kind")
		}
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "get-file", Method: http.MethodGet, Path: "/files/{file_id}",
		Summary: "Get authorized file metadata", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileResourceInput) (*respx.Body[fileEntryView], error) {
		fileID, err := fileID(input.FileID)
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
	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "get-file-by-path", Method: http.MethodGet, Path: runtimeFilePath,
		Summary: "Get authorized file metadata by a root-relative path", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *filePathInput) (*respx.Body[fileEntryView], error) {
		rootID, err := fileID(input.RootFileID)
		if err != nil {
			return nil, err
		}
		result, err := service.getFileEntryByPath(ctx, rootID, input.Path)
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})
	documentFilePath(api, "/me"+runtimeFilePath, "/me/files/{root_file_id}/-/{path}")
	const tenantPrefix = "/tenants/{tenant_id}"
	documentFilePath(api, tenantPrefix+runtimeFilePath, tenantPrefix+"/files/{root_file_id}/-/{path}")
	const entityPrefix = "/tenants/{tenant_id}/entities/{entity_id}"
	documentFilePath(api, entityPrefix+runtimeFilePath, entityPrefix+"/files/{root_file_id}/-/{path}")

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "download-file", Method: http.MethodGet, Path: "/files/{file_id}/content",
		Summary: "Download file content", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "download", FilterData: true}, func(ctx context.Context, input *fileResourceInput) (*huma.StreamResponse, error) {
		fileID, err := fileID(input.FileID)
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

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "update-file", Method: http.MethodPatch, Path: "/files/{file_id}",
		Summary: "Update a file or directory", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "view", FilterData: true}, func(ctx context.Context, input *fileUpdateInput) (*respx.Body[fileEntryView], error) {
		fileID, err := fileID(input.FileID)
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
			return nil, huma.Error422UnprocessableEntity("invalid_file_update")
		}
		if err != nil {
			return nil, fileHTTPError(err)
		}
		return respx.OK(ctx, result), nil
	})

	authz.RegisterTenantEntityOrAuthenticated(registrar, api, huma.Operation{
		OperationID: "delete-file", Method: http.MethodDelete, Path: "/files/{file_id}",
		Summary: "Move a file or directory to trash", Tags: []string{"tenants"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "file", Verb: "delete", FilterData: true}, func(ctx context.Context, input *fileDeleteInput) (*respx.Body[fileTrashResult], error) {
		fileID, err := fileID(input.FileID)
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
		Summary: "Copy or move files synchronously", Tags: []string{"tenants"}, DefaultStatus: http.StatusCreated,
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
		cursor, err = fileID(after)
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
		return immediateOperationRequest{}, huma.Error422UnprocessableEntity("invalid_file_operation")
	}
	destinationID, err := fileID(input.Body.DestinationID)
	if err != nil {
		return immediateOperationRequest{}, err
	}
	sources := make([]guid.ID, len(input.Body.Sources))
	for index, raw := range input.Body.Sources {
		sources[index], err = fileID(raw)
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

func fileID(raw string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(raw))
	if err != nil || id <= 0 {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func fileHTTPError(err error) error {
	var conflicts *immediateConflicts
	switch {
	case errors.As(err, &conflicts):
		return &fileConflictHTTPError{
			status: http.StatusConflict, Code: http.StatusConflict, Message: "file_operation_conflict",
			Meta: respx.Meta{RequestAt: time.Now().UTC()}, Data: conflicts,
		}
	case errors.Is(err, errOperationForbidden):
		return huma.Error403Forbidden("forbidden")
	case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return huma.Error404NotFound("not_found")
	case errors.Is(err, errRestoreNameConflict):
		return huma.Error409Conflict("file_restore_name_conflict")
	case errors.Is(err, errTreeConflict), errors.Is(err, errVersionConflict), errors.Is(err, errOperationKey), errors.Is(err, errOperationState), errors.Is(err, core.ErrAuthorizationChanged):
		return huma.Error409Conflict("file_state_changed")
	case errors.Is(err, errOperationInput), errors.Is(err, errFileACL), errors.Is(err, errHierarchy), errors.Is(err, errRootUnbound),
		errors.Is(err, ErrName), errors.Is(err, ErrLength), errors.Is(err, ErrInvalidKey):
		return huma.Error422UnprocessableEntity("invalid_file_request")
	case errors.Is(err, errOperationUnavailable), errors.Is(err, context.DeadlineExceeded):
		return huma.Error503ServiceUnavailable("file_service_unavailable")
	default:
		return err
	}
}
