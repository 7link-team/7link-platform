package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const fileRouteManifest = `
create-file-operation|POST|/file-operations
create-file|POST|/files
delete-file|DELETE|/files/{file_id}
download-file|GET|/files/{file_id}/content
get-file-by-path|GET|/files/{root_file_id}/-/{path}
get-file|GET|/files/{file_id}
list-file-items|GET|/files/{file_id}/items
list-files|GET|/files
update-file|PATCH|/files/{file_id}`

func fileDeclarationAPI(t *testing.T) (huma.API, *authz.Registry) {
	t.Helper()
	registry := authz.MustRegistry()
	for _, action := range fileAuthorizationActions() {
		require.NoError(t, registry.Register(action))
	}
	_, api := humatest.New(t)
	registerFileHTTP(api, &operationService{maxUploadBytes: 100 << 20}, authz.NewDeclarationOnlyRegistrar(registry))
	return api, registry
}

func TestFileHTTPContract(t *testing.T) {
	api, registry := fileDeclarationAPI(t)
	manifest := make([]string, 0, 8)
	for path, item := range api.OpenAPI().Paths {
		require.NotContains(t, path, "/nodes")
		require.NotContains(t, path, "/directories")
		require.NotContains(t, path, "file-root")
		require.NotContains(t, path, "files/roots")
		require.NotContains(t, path, "node_id")
		require.NotContains(t, path, "directory_id")
		for method, operation := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if operation == nil {
				continue
			}
			manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			require.Equal(t, []string{"files"}, operation.Tags)
			require.Contains(t, operation.Extensions[authz.GuardExtensionKey], filePermissionPrefix)
			require.Equal(t, true, operation.Extensions[authz.DataFilterExtensionKey])
			require.NotEmpty(t, operation.Security)
			selectors := map[string]bool{}
			for _, parameter := range operation.Parameters {
				if parameter.In == "header" && (parameter.Name == authz.TenantHeader || parameter.Name == authz.EntityHeader) {
					selectors[parameter.Name] = parameter.Required
				}
			}
			require.Equal(t, map[string]bool{authz.TenantHeader: false, authz.EntityHeader: false}, selectors)
		}
	}
	sort.Strings(manifest)
	want := strings.Fields(fileRouteManifest)
	sort.Strings(want)
	require.Equal(t, want, manifest)
	require.NoError(t, authz.ValidateOperations(api, registry))

	children := api.OpenAPI().Paths["/files/{file_id}/items"].Get
	for _, status := range []string{"404", "409", "422"} {
		require.Contains(t, children.Responses, status)
	}
	require.Nil(t, api.OpenAPI().Paths["/files/operations"])
	require.NotNil(t, api.OpenAPI().Paths["/file-operations"].Post)
}

func TestFileHTTPInputAndErrorMapping(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "text"} {
		_, err := fileID(raw)
		require.Error(t, err, raw)
		require.Equal(t, http.StatusUnprocessableEntity, err.(huma.StatusError).GetStatus())
	}
	require.Equal(t, guid.ID(42), requireFileID(t, " 42 "))
	for _, test := range []struct {
		err    error
		status int
	}{
		{errOperationForbidden, http.StatusForbidden},
		{ErrNotFound, http.StatusNotFound},
		{errRestoreNameConflict, http.StatusConflict},
		{errVersionConflict, http.StatusConflict},
		{errOperationInput, http.StatusUnprocessableEntity},
		{ErrName, http.StatusUnprocessableEntity},
		{ErrLength, http.StatusUnprocessableEntity},
		{ErrInvalidKey, http.StatusUnprocessableEntity},
		{errOperationUnavailable, http.StatusServiceUnavailable},
	} {
		require.Equal(t, test.status, fileHTTPError(test.err).(huma.StatusError).GetStatus())
	}
	var restoreConflict *huma.ErrorModel
	require.ErrorAs(t, fileHTTPError(errRestoreNameConflict), &restoreConflict)
	require.Equal(t, "file_restore_name_conflict", restoreConflict.Detail)

	conflicts := &immediateConflicts{SpaceRevision: 7, Conflicts: []immediateConflict{{Key: strings.Repeat("a", 64), Kind: "file"}}}
	mapped := fileHTTPError(conflicts)
	var response *fileConflictHTTPError
	require.ErrorAs(t, mapped, &response)
	require.Same(t, conflicts, response.Data)
	require.Equal(t, http.StatusConflict, response.GetStatus())
}

func requireFileID(t *testing.T, raw string) guid.ID {
	t.Helper()
	id, err := fileID(raw)
	require.NoError(t, err)
	return id
}

type fileHTTPVerifier struct{}

func (fileHTTPVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{Subject: "70", PrincipalID: 70, SubjectType: authn.SubjectTypePrincipal}, nil
}

type fileHTTPAuthorizer struct{}

func (fileHTTPAuthorizer) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (fileHTTPAuthorizer) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

func (fileHTTPAuthorizer) EntityConstraint(_ context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
	if tenantID != 90 || entityID != 80 || !strings.HasPrefix(permission, filePermissionPrefix) || subject != 70 {
		return authz.DataConstraint{}, errors.New("unexpected file HTTP authorization input")
	}
	return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
}

type fileHTTPMembership struct{}

func (fileHTTPMembership) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func newFileHTTPAPI(t *testing.T, service *operationService) humatest.TestAPI {
	t.Helper()
	registry := authz.MustRegistry()
	for _, action := range fileAuthorizationActions() {
		require.NoError(t, registry.Register(action))
	}
	registrar := authz.NewRegistrar(registry, fileHTTPVerifier{}, fileHTTPAuthorizer{}, fileHTTPMembership{})
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("Files test", "1"))
	registerFileHTTP(api, service, registrar)
	return humatest.Wrap(t, api)
}

func TestFileHTTPAuthorizedBasicRoutes(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)
	headers := []any{"Authorization: Bearer test", authz.TenantHeader + ": 90", authz.EntityHeader + ": 80"}

	roots := api.Get("/files?limit=1", headers...)
	require.Equal(t, http.StatusOK, roots.Code, roots.Body.String())
	var rootEnvelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(roots.Body.Bytes(), &rootEnvelope))
	require.Len(t, rootEnvelope.Data.Items, 1)
	require.Equal(t, "directory", rootEnvelope.Data.Items[0].Kind)
	require.Equal(t, "tenant", rootEnvelope.Data.Items[0].Scope)

	children := api.Get("/files/2/items?limit=1", headers...)
	require.Equal(t, http.StatusOK, children.Code, children.Body.String())
	var childrenEnvelope respx.Envelope[fileChildrenPage]
	require.NoError(t, json.Unmarshal(children.Body.Bytes(), &childrenEnvelope))
	require.Len(t, childrenEnvelope.Data.Items, 1)

	metadata := api.Get("/files/32", headers...)
	require.Equal(t, http.StatusOK, metadata.Code, metadata.Body.String())
	byPath := api.Get("/files/2/-/Source/Shared/root.txt", headers...)
	require.Equal(t, http.StatusOK, byPath.Code, byPath.Body.String())

	created := postFileForm(t, api, headers, map[string]string{"parent_id": "2", "kind": "directory", "name": "Contracts"}, "", "")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var createdEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdEnvelope))
	require.Equal(t, "Contracts", createdEnvelope.Data.Name)
	require.Equal(t, "directory", createdEnvelope.Data.Kind)

	personal := postFileForm(t, api, headers, map[string]string{"kind": "directory", "scope": "personal"}, "", "")
	require.Equal(t, http.StatusCreated, personal.Code, personal.Body.String())
	var personalEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(personal.Body.Bytes(), &personalEnvelope))
	require.Equal(t, "directory", personalEnvelope.Data.Kind)
	require.Equal(t, "personal", personalEnvelope.Data.Scope)

	uploaded := postFileForm(t, api, headers, map[string]string{"parent_id": createdEnvelope.Data.ID.String(), "kind": "file"}, "report.txt", "payload")
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	var uploadedEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(uploaded.Body.Bytes(), &uploadedEnvelope))

	renamedFile := api.Patch("/files/"+uploadedEnvelope.Data.ID.String(), append(headers, map[string]any{
		"name": "final.txt", "revision": uploadedEnvelope.Data.Revision,
	})...)
	require.Equal(t, http.StatusOK, renamedFile.Code, renamedFile.Body.String())
	var renamedFileEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(renamedFile.Body.Bytes(), &renamedFileEnvelope))
	renamedDirectory := api.Patch("/files/"+createdEnvelope.Data.ID.String(), append(headers, map[string]any{
		"name": "Agreements", "revision": createdEnvelope.Data.Revision,
	})...)
	require.Equal(t, http.StatusOK, renamedDirectory.Code, renamedDirectory.Body.String())
	var renamedDirectoryEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(renamedDirectory.Body.Bytes(), &renamedDirectoryEnvelope))

	deletedFile := api.Delete("/files/"+renamedFileEnvelope.Data.ID.String()+"?revision="+strconv.FormatInt(renamedFileEnvelope.Data.Revision, 10), headers...)
	require.Equal(t, http.StatusOK, deletedFile.Code, deletedFile.Body.String())
	deletedDirectory := api.Delete("/files/"+renamedDirectoryEnvelope.Data.ID.String()+"?revision="+strconv.FormatInt(renamedDirectoryEnvelope.Data.Revision, 10), headers...)
	require.Equal(t, http.StatusOK, deletedDirectory.Code, deletedDirectory.Body.String())

	invalid := api.Get("/files/not-an-id/items", headers...)
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code, invalid.Body.String())
	invalidRoot := postFileForm(t, api, headers, map[string]string{"kind": "directory"}, "", "")
	require.Equal(t, http.StatusUnprocessableEntity, invalidRoot.Code, invalidRoot.Body.String())
}

func TestFileHTTPAccountContextUsesSameResourcesWithoutTenantHeaders(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)

	empty := api.Get("/files?limit=100")
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	var emptyEnvelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(empty.Body.Bytes(), &emptyEnvelope))
	require.Empty(t, emptyEnvelope.Data.Items)

	created := postFileForm(t, api, nil, map[string]string{"kind": "directory", "scope": "account"}, "", "")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var createdEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdEnvelope))
	require.Equal(t, "account", createdEnvelope.Data.Scope)
	require.Equal(t, "Account files", createdEnvelope.Data.Name)
	requireOptionalID(t, createdEnvelope.Data.PrincipalID, 70)
	require.Nil(t, createdEnvelope.Data.TenantID)

	listed := api.Get("/files?limit=100")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var listedEnvelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listedEnvelope))
	require.Equal(t, []fileEntryView{createdEnvelope.Data}, listedEnvelope.Data.Items)

	directory := postFileForm(t, api, nil, map[string]string{
		"parent_id": createdEnvelope.Data.ID.String(), "kind": "directory", "name": "Private",
	}, "", "")
	require.Equal(t, http.StatusCreated, directory.Code, directory.Body.String())
	var directoryEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(directory.Body.Bytes(), &directoryEnvelope))
	require.Equal(t, "Private", directoryEnvelope.Data.Name)

	uploaded := postFileForm(t, api, nil, map[string]string{
		"parent_id": directoryEnvelope.Data.ID.String(), "kind": "file",
	}, "account.txt", "account payload")
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	var uploadedEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(uploaded.Body.Bytes(), &uploadedEnvelope))

	copied := api.Post("/file-operations",
		"Idempotency-Key: account-copy",
		map[string]any{
			"action": "copy", "destination_id": createdEnvelope.Data.ID.String(),
			"sources":     []string{uploadedEnvelope.Data.ID.String()},
			"file_policy": "rename", "directory_policy": "rename", "type_policy": "rename",
		})
	require.Equal(t, http.StatusCreated, copied.Code, copied.Body.String())
	var copiedEnvelope respx.Envelope[operationResult]
	require.NoError(t, json.Unmarshal(copied.Body.Bytes(), &copiedEnvelope))
	require.Equal(t, 1, copiedEnvelope.Data.Created)
	require.Len(t, copiedEnvelope.Data.Roots, 1)

	moved := api.Post("/file-operations",
		"Idempotency-Key: account-move",
		map[string]any{
			"action": "move", "destination_id": directoryEnvelope.Data.ID.String(),
			"sources":     []string{copiedEnvelope.Data.Roots[0].String()},
			"file_policy": "rename", "directory_policy": "rename", "type_policy": "rename",
		})
	require.Equal(t, http.StatusCreated, moved.Code, moved.Body.String())
	var movedEnvelope respx.Envelope[operationResult]
	require.NoError(t, json.Unmarshal(moved.Body.Bytes(), &movedEnvelope))
	require.Equal(t, 1, movedEnvelope.Data.Moved)

	downloaded := api.Get("/files/" + uploadedEnvelope.Data.ID.String() + "/content")
	require.Equal(t, http.StatusOK, downloaded.Code, downloaded.Body.String())
	require.Equal(t, "account payload", downloaded.Body.String())
	require.Contains(t, downloaded.Header().Get("Content-Disposition"), "account.txt")

	renamed := api.Patch("/files/"+uploadedEnvelope.Data.ID.String(), map[string]any{
		"name": "private.txt", "revision": uploadedEnvelope.Data.Revision,
	})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	var renamedEnvelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(renamed.Body.Bytes(), &renamedEnvelope))
	require.Equal(t, "private.txt", renamedEnvelope.Data.Name)

	deletedFile := api.Delete("/files/" + renamedEnvelope.Data.ID.String() + "?revision=" +
		strconv.FormatInt(renamedEnvelope.Data.Revision, 10))
	require.Equal(t, http.StatusOK, deletedFile.Code, deletedFile.Body.String())
	deletedDirectory := api.Delete("/files/" + directoryEnvelope.Data.ID.String() + "?revision=" +
		strconv.FormatInt(directoryEnvelope.Data.Revision, 10))
	require.Equal(t, http.StatusOK, deletedDirectory.Code, deletedDirectory.Body.String())

	trash := api.Get("/files?state=trashed&limit=100")
	require.Equal(t, http.StatusOK, trash.Code, trash.Body.String())
	var trashEnvelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(trash.Body.Bytes(), &trashEnvelope))
	require.Len(t, trashEnvelope.Data.Items, 2)
	trashedByID := make(map[guid.ID]fileEntryView, len(trashEnvelope.Data.Items))
	for _, item := range trashEnvelope.Data.Items {
		trashedByID[item.ID] = item
	}
	for _, id := range []guid.ID{directoryEnvelope.Data.ID, uploadedEnvelope.Data.ID} {
		item, ok := trashedByID[id]
		require.True(t, ok)
		restored := api.Patch("/files/"+id.String(), map[string]any{
			"state": "active", "revision": item.Revision,
		})
		require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	}
	emptyTrash := api.Get("/files?state=trashed&limit=100")
	require.Equal(t, http.StatusOK, emptyTrash.Code, emptyTrash.Body.String())
	require.NoError(t, json.Unmarshal(emptyTrash.Body.Bytes(), &trashEnvelope))
	require.Empty(t, trashEnvelope.Data.Items)

	var accountAuditCount int64
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("tenant_id = ?", 0).Count(&accountAuditCount).Error)
	require.Equal(t, int64(11), accountAuditCount)
	var foreignAuditCount int64
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("tenant_id <> ?", 0).Count(&foreignAuditCount).Error)
	require.Zero(t, foreignAuditCount)

	tenantHeaders := []any{authz.TenantHeader + ": 90", authz.EntityHeader + ": 80"}
	require.Equal(t, http.StatusForbidden, api.Get("/files/"+createdEnvelope.Data.ID.String(), tenantHeaders...).Code)
	require.Equal(t, http.StatusForbidden,
		postFileForm(t, api, tenantHeaders, map[string]string{"kind": "directory", "scope": "account"}, "", "").Code)
	require.Equal(t, http.StatusForbidden,
		postFileForm(t, api, nil, map[string]string{"kind": "directory", "scope": "personal"}, "", "").Code)
	for _, header := range []any{authz.TenantHeader + ": 90", authz.EntityHeader + ": 80"} {
		response := api.Get("/files", header)
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Contains(t, response.Body.String(), "incomplete_resource_context")
	}
}

func postFileForm(t *testing.T, api humatest.TestAPI, headers []any, fields map[string]string, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		require.NoError(t, form.WriteField(name, value))
	}
	if filename != "" {
		part, err := form.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, form.Close())
	args := append([]any{}, headers...)
	args = append(args, "Content-Type: "+form.FormDataContentType(), bytes.NewReader(body.Bytes()))
	return api.Post("/files", args...)
}
