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

	filesi18n "7link.com/platform/internal/files/i18n"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const fileRouteManifest = `
list-system-files|GET|/system/files
create-system-file|POST|/system/files
create-system-file-operation|POST|/system/file-operations
create-file-operation-self|POST|/principals/{principal_id}/file-operations
create-file-operation-tenant|POST|/tenants/{tenant_id}/file-operations
create-file-operation|POST|/tenants/{tenant_id}/entities/{entity_id}/file-operations
create-my-file|POST|/principals/{principal_id}/files
create-tenant-file|POST|/tenants/{tenant_id}/files
create-entity-file|POST|/entities/{entity_id}/files
create-department-file|POST|/departments/{department_id}/files
delete-file|DELETE|/files/{file_id}
download-file|GET|/files/{file_id}/content
get-file-by-path|GET|/files/{root_file_id}/-/{path}
get-file|GET|/files/{file_id}
list-file-items|GET|/files/{file_id}/items
list-my-files|GET|/principals/{principal_id}/files
list-tenant-files|GET|/tenants/{tenant_id}/files
list-entity-files|GET|/entities/{entity_id}/files
list-department-files|GET|/departments/{department_id}/files
update-file|PATCH|/files/{file_id}`

func fileDeclarationAPI(t *testing.T) (huma.API, *authz.Registry) {
	t.Helper()
	registry := authz.MustRegistry()
	for _, action := range fileAuthorizationActions() {
		require.NoError(t, registry.Register(action))
	}
	_, api := humatest.New(t)
	owner := func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 90, EntityID: 80}, true, nil
	}
	registerFileHTTP(api, &operationService{maxUploadBytes: 100 << 20}, authz.NewDeclarationOnlyRegistrar(registry), owner, owner, owner)
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
			// File routes are served under five different owners, so deriving
			// the section from the path scattered one module across five of
			// them. The module publishes one section.
			if strings.HasPrefix(path, "/system/") {
				require.Equal(t, []string{"system"}, operation.Tags)
				require.True(t, authz.IsPlatformOperation(operation))
			} else {
				require.Equal(t, []string{"files"}, operation.Tags)
			}
			if strings.HasPrefix(path, "/system/") {
				require.Contains(t, operation.Extensions[authz.GuardExtensionKey], "system_file_")
			} else if operation.OperationID == "list-entity-files" {
				require.True(t, authz.IsTenantMemberOperation(operation))
				require.True(t, authz.IsOwnedResourceOperation(operation))
			} else if strings.HasPrefix(path, "/principals/{principal_id}/") {
				require.True(t, authz.IsAuthenticatedOperation(operation))
			} else {
				require.Contains(t, operation.Extensions[authz.GuardExtensionKey], filePermissionPrefix)
				require.Equal(t, true, operation.Extensions[authz.DataFilterExtensionKey])
			}
			require.NotEmpty(t, operation.Security)
			selectors := map[string]bool{}
			for _, parameter := range operation.Parameters {
				if parameter.In == "path" && (parameter.Name == authz.TenantPath || parameter.Name == authz.EntityPath) {
					selectors[parameter.Name] = parameter.Required
				}
			}
			if strings.HasPrefix(path, "/principals/{principal_id}/") {
				require.Empty(t, selectors)
			} else if strings.Contains(path, "/entities/") {
				if authz.IsOwnedResourceOperation(operation) {
					require.Equal(t, map[string]bool{authz.EntityPath: true}, selectors)
				} else {
					require.Equal(t, map[string]bool{authz.TenantPath: true, authz.EntityPath: true}, selectors)
				}
			} else if strings.HasPrefix(path, "/tenants/") {
				require.Equal(t, map[string]bool{authz.TenantPath: true}, selectors)
			} else {
				require.Empty(t, selectors)
			}
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
	require.NotNil(t, api.OpenAPI().Paths["/principals/{principal_id}/file-operations"].Post)
	for _, path := range []string{"/principals/{principal_id}/files", "/tenants/{tenant_id}/files", "/entities/{entity_id}/files", "/departments/{department_id}/files"} {
		queries := make(map[string]*huma.Param)
		for _, parameter := range api.OpenAPI().Paths[path].Get.Parameters {
			if parameter.In == "query" {
				queries[parameter.Name] = parameter
			}
		}
		require.Len(t, queries, 3, path)
		require.Contains(t, queries, "after", path)
		require.Contains(t, queries, "limit", path)
		require.Contains(t, queries, "state", path)
		require.Equal(t, "integer", queries["limit"].Schema.Type)
		require.Equal(t, []any{"active", "trashed"}, queries["state"].Schema.Enum)
	}
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

// fileHTTPVerifier answers with the caller a test wants. A selected tenant is
// part of what it answers with, because whether a route drops that tenant is
// behavior the route is responsible for.
type fileHTTPVerifier struct{ tenantID guid.ID }

func (v fileHTTPVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{
		Subject: "70", PrincipalID: 70, SubjectType: authn.SubjectTypePrincipal, TenantID: v.tenantID,
	}, nil
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

func newFileHTTPAPI(t *testing.T, service *operationService, selected ...guid.ID) humatest.TestAPI {
	t.Helper()
	registry := authz.MustRegistry()
	for _, action := range fileAuthorizationActions() {
		require.NoError(t, registry.Register(action))
	}
	verifier := fileHTTPVerifier{}
	if len(selected) == 1 {
		verifier.tenantID = selected[0]
	}
	registrar := authz.NewRegistrar(registry, verifier, fileHTTPAuthorizer{}, fileHTTPMembership{})
	api := humachi.New(chi.NewRouter(), huma.DefaultConfig("Files test", "1"))
	owner := func(ctx context.Context, fileID guid.ID) (authz.ResourceOwner, bool, error) {
		db := service.runtime.Database().Write()
		tree := directoryTree{prefix: service.runtime.TablePrefix()}
		q := query.Bind(db, tree.prefix)
		node, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(fileID)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authz.ResourceOwner{}, false, nil
		}
		if err != nil {
			return authz.ResourceOwner{}, false, err
		}
		resolved, err := tree.resourceOwner(ctx, db, node.SpaceID, node.ID)
		if err != nil {
			return authz.ResourceOwner{}, false, err
		}
		switch resolved.Scope {
		case "system":
			return authz.ResourceOwner{ScopeType: "system"}, true, nil
		case "account":
			return authz.ResourceOwner{PrincipalID: resolved.ID}, true, nil
		case "tenant":
			return authz.ResourceOwner{TenantID: resolved.ID}, true, nil
		case "entity", "department":
			return authz.ResourceOwner{TenantID: 90, EntityID: 80}, true, nil
		default:
			return authz.ResourceOwner{}, false, nil
		}
	}
	entityOwner := func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{TenantID: 90, EntityID: 80}, true, nil
	}
	registerFileHTTP(api, service, registrar, owner, entityOwner, entityOwner)
	return humatest.Wrap(t, api)
}

func TestFileHTTPAuthorizedBasicRoutes(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(
		q.Node.RootScope.Value("entity"), q.Node.RootOwnerID.Value(guid.ID(80)),
	)
	require.NoError(t, err)
	api := newFileHTTPAPI(t, h.service)
	headers := []any{"Authorization: Bearer test"}
	base := "/entities/80/files"

	roots := api.Get(base+"?limit=1", headers...)
	require.Equal(t, http.StatusOK, roots.Code, roots.Body.String())
	var rootEnvelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(roots.Body.Bytes(), &rootEnvelope))
	require.Len(t, rootEnvelope.Data.Items, 1)
	require.Equal(t, "directory", rootEnvelope.Data.Items[0].Kind)
	require.Equal(t, "entity", rootEnvelope.Data.Items[0].Scope)

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

	// "personal" was a second personal space inside each tenant. It is gone,
	// and the contract refuses it rather than quietly accepting a scope no
	// root can have.
	removed := postFileForm(t, api, headers, map[string]string{"kind": "directory", "scope": "personal"}, "", "")
	require.Equal(t, http.StatusUnprocessableEntity, removed.Code, removed.Body.String())

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

func TestFileHTTPTenantContextUsesTenantPathWithoutEntity(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)

	roots := api.Get("/tenants/90/files?limit=100", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, roots.Code, roots.Body.String())
	var envelope respx.Envelope[filePage]
	require.NoError(t, json.Unmarshal(roots.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.Items)
	for _, root := range envelope.Data.Items {
		require.NotEqual(t, "account", root.Scope)
		requireOptionalID(t, root.TenantID, 90)
	}
	require.Equal(t, http.StatusNotFound, api.Get("/files?limit=100").Code)
}

func TestFileHTTPAccountContextUsesSameResourcesWithoutTenantHeaders(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)

	empty := api.Get("/principals/" + "70" + "/files?limit=100")
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

	listed := api.Get("/principals/" + "70" + "/files?limit=100")
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

	copied := api.Post("/principals/"+"70"+"/file-operations",
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

	moved := api.Post("/principals/"+"70"+"/file-operations",
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
	require.Contains(t, downloaded.Header().Get("Content-Disposition"), "attachment")
	inline := api.Get("/files/" + uploadedEnvelope.Data.ID.String() + "/content?disposition=inline")
	require.Equal(t, http.StatusOK, inline.Code, inline.Body.String())
	require.Contains(t, inline.Header().Get("Content-Disposition"), "inline")

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

	trash := api.Get("/principals/" + "70" + "/files?state=trashed&limit=100")
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
	emptyTrash := api.Get("/principals/" + "70" + "/files?state=trashed&limit=100")
	require.Equal(t, http.StatusOK, emptyTrash.Code, emptyTrash.Body.String())
	require.NoError(t, json.Unmarshal(emptyTrash.Body.Bytes(), &trashEnvelope))
	require.Empty(t, trashEnvelope.Data.Items)

	var accountAuditCount int64
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("tenant_id = ?", 0).Count(&accountAuditCount).Error)
	require.Equal(t, int64(12), accountAuditCount)
	var foreignAuditCount int64
	require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_events").
		Where("tenant_id <> ?", 0).Count(&foreignAuditCount).Error)
	require.Zero(t, foreignAuditCount)

	require.Equal(t, http.StatusNotFound, api.Get("/tenants/90/entities/80/files/"+createdEnvelope.Data.ID.String()).Code)
	require.Equal(t, http.StatusUnprocessableEntity,
		postFileForm(t, api, []any{"Authorization: Bearer test"}, map[string]string{"kind": "directory", "scope": "account"}, "", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity,
		postFileForm(t, api, nil, map[string]string{"kind": "directory", "scope": "personal"}, "", "").Code)
	for _, path := range []string{"/tenants/90/entities//files", "/tenants//entities/80/files"} {
		response := api.Get(path)
		require.Equal(t, http.StatusNotFound, response.Code)
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
	path := "/principals/" + "70" + "/files"
	if len(headers) > 0 {
		path = "/entities/80/files"
	}
	return api.Post(path, args...)
}

// TestFileHTTPRoutesReportStorageFailuresAsServerErrors walks the read routes
// with the tree tables gone. Each has to answer 5xx. Reporting an outage as an
// empty listing or a 404 would tell the caller their files do not exist when
// in fact nothing was ever read.
func TestFileHTTPRoutesReportStorageFailuresAsServerErrors(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(
		q.Node.RootScope.Value("entity"), q.Node.RootOwnerID.Value(guid.ID(80)),
	)
	require.NoError(t, err)
	api := newFileHTTPAPI(t, h.service)
	headers := []any{"Authorization: Bearer test"}

	// Positive control: these answer 200 before the tables are dropped, so the
	// 5xx below is attributable to the outage and not to the request shape.
	require.Equal(t, http.StatusOK, api.Get("/entities/80/files?limit=1", headers...).Code)
	require.Equal(t, http.StatusOK, api.Get("/files/2/items?limit=1", headers...).Code)

	require.NoError(t, h.db.Exec("DROP TABLE "+h.tree.prefix+"file_node_closure").Error)

	// Only routes that actually walk the tree are listed. The tenant and
	// personal collections own no root in this fixture, so they answer an empty
	// page without ever reaching the dropped tables; asserting a failure there
	// would assert something untrue.
	for name, target := range map[string]string{
		"entity roots": "/entities/80/files?limit=1",
		"children":     "/files/2/items?limit=1",
		"metadata":     "/files/32",
		"content":      "/files/32/content",
		"path lookup":  "/files/2/-/child.txt",
	} {
		response := api.Get(target, headers...)
		assert.GreaterOrEqual(t, response.Code, http.StatusInternalServerError,
			"%s must report the outage as a server error, got %d: %s", name, response.Code, response.Body.String())
	}
}

func TestFileHTTPHelpersRejectMalformedRequests(t *testing.T) {
	assertHTTPError := func(t *testing.T, err error, status int, detail string) {
		t.Helper()
		var response *huma.ErrorModel
		require.ErrorAs(t, err, &response)
		require.Equal(t, status, response.GetStatus())
		require.Equal(t, detail, response.Detail)
	}

	conflict := &fileConflictHTTPError{status: http.StatusConflict, Message: "file conflict"}
	require.Equal(t, "file conflict", conflict.Error())
	require.Equal(t, http.StatusConflict, conflict.GetStatus())

	page, err := browserPageOf(" 32 ", 7)
	require.NoError(t, err)
	require.Equal(t, browserPageRequest{After: 32, Limit: 7}, page)
	page, err = browserPageOf("", 0)
	require.NoError(t, err)
	require.Equal(t, browserPageRequest{Limit: defaultBrowserPage}, page)
	_, err = browserPageOf("bad", 10)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, respx.InvalidID)
	_, err = browserPageOf("", maxBrowserPage+1)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, filesi18n.InvalidFileRequest)

	h := newOperationServiceHarness(t)
	for name, test := range map[string]struct {
		form      *fileCreateForm
		allowRoot bool
		status    int
		detail    string
	}{
		"missing form":         {nil, true, http.StatusUnprocessableEntity, filesi18n.InvalidFileContent},
		"root not allowed":     {&fileCreateForm{Kind: "directory", Scope: "account"}, false, http.StatusUnprocessableEntity, filesi18n.InvalidFileRoot},
		"named root":           {&fileCreateForm{Kind: "directory", Scope: "account", Name: "named"}, true, http.StatusUnprocessableEntity, filesi18n.InvalidFileRoot},
		"scoped child":         {&fileCreateForm{ParentID: "2", Kind: "directory", Scope: "account"}, false, http.StatusUnprocessableEntity, filesi18n.InvalidFileScope},
		"invalid parent":       {&fileCreateForm{ParentID: "bad", Kind: "directory"}, false, http.StatusUnprocessableEntity, respx.InvalidID},
		"directory content":    {&fileCreateForm{ParentID: "2", Kind: "directory", File: huma.FormFile{IsSet: true}}, false, http.StatusUnprocessableEntity, filesi18n.DirectoryContentNotAllowed},
		"missing file":         {&fileCreateForm{ParentID: "2", Kind: "file"}, false, http.StatusUnprocessableEntity, filesi18n.InvalidFileContent},
		"unsupported kind":     {&fileCreateForm{ParentID: "2", Kind: "link"}, false, http.StatusUnprocessableEntity, filesi18n.InvalidFileKind},
		"invalid directory":    {&fileCreateForm{ParentID: "2", Kind: "directory", Name: "."}, false, http.StatusUnprocessableEntity, filesi18n.InvalidFileRequest},
		"foreign account root": {&fileCreateForm{Kind: "directory", Scope: "account"}, true, http.StatusForbidden, respx.Forbidden},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := createFile(h.ctx, h.service, test.form, test.allowRoot)
			require.Nil(t, body)
			assertHTTPError(t, err, test.status, test.detail)
		})
	}

	_, err = fileOperationRequestOf(nil)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, filesi18n.InvalidFileOperation)
	badDestination := &fileOperationInput{IdempotencyKey: "operation"}
	badDestination.Body.DestinationID = "bad"
	_, err = fileOperationRequestOf(badDestination)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, respx.InvalidID)
	badSource := &fileOperationInput{IdempotencyKey: "operation"}
	badSource.Body.DestinationID = "10"
	badSource.Body.Sources = []string{"bad"}
	_, err = fileOperationRequestOf(badSource)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, respx.InvalidID)

	valid := &fileOperationInput{IdempotencyKey: "operation"}
	valid.Body.Action = "copy"
	valid.Body.DestinationID = "10"
	valid.Body.Sources = []string{"12", "11"}
	valid.Body.FilePolicy = "ask"
	valid.Body.DirectoryPolicy = "ask"
	valid.Body.TypePolicy = "ask"
	valid.Body.ObservedRevision = 7
	valid.Body.Decisions = []fileOperationDecisionInput{
		{ConflictKey: strings.Repeat("b", 64), Policy: "rename"},
		{ConflictKey: strings.Repeat("a", 64), Policy: "skip"},
	}
	request, err := fileOperationRequestOf(valid)
	require.NoError(t, err)
	require.Zero(t, request.SpaceID, "the public request cannot select a storage space")
	require.Equal(t, guid.ID(10), request.DestinationID)
	require.Equal(t, []guid.ID{11, 12}, request.Sources)
	require.Equal(t, []immediateDecision{
		{ConflictKey: strings.Repeat("a", 64), Policy: "skip"},
		{ConflictKey: strings.Repeat("b", 64), Policy: "rename"},
	}, request.Decisions)

	valid.IdempotencyKey = ""
	_, err = fileOperationRequestOf(valid)
	assertHTTPError(t, err, http.StatusUnprocessableEntity, filesi18n.InvalidFileRequest)
}

func TestFileHTTPGlobalRoutesResolveOwnersBeforeHandlers(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)
	headers := []any{"Authorization: Bearer test"}

	missing := api.Get("/files/999999", headers...)
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
	invalid := api.Get("/files/not-an-id", headers...)
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code, invalid.Body.String())

	fault := failTableAfter(t, h.service.runtime.Database().Write(), "file_nodes", 0)
	unavailable := api.Get("/files/32", headers...)
	require.True(t, fault.Fired(), "the owner resolver must reach the injected storage failure")
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code, unavailable.Body.String())
}

func TestFileHTTPGlobalRoutesValidateItemAndMutationBehavior(t *testing.T) {
	h := newOperationServiceHarness(t)
	api := newFileHTTPAPI(t, h.service)
	headers := []any{"Authorization: Bearer test"}

	invalidPage := api.Get("/files/2/items?after=bad", headers...)
	require.Equal(t, http.StatusUnprocessableEntity, invalidPage.Code, invalidPage.Body.String())
	fileItems := api.Get("/files/32/items", headers...)
	require.Equal(t, http.StatusNotFound, fileItems.Code, fileItems.Body.String())
	throughFile := api.Get("/files/2/-/Source/Shared/root.txt/child", headers...)
	require.Equal(t, http.StatusNotFound, throughFile.Code, throughFile.Body.String())
	directoryContent := api.Get("/files/2/content", headers...)
	require.Equal(t, http.StatusNotFound, directoryContent.Code, directoryContent.Body.String())

	invalidUpdate := api.Patch("/files/32", append(headers, map[string]any{"revision": 1})...)
	require.Equal(t, http.StatusUnprocessableEntity, invalidUpdate.Code, invalidUpdate.Body.String())
	staleRename := api.Patch("/files/32", append(headers, map[string]any{"name": "stale.txt", "revision": 999})...)
	require.Equal(t, http.StatusConflict, staleRename.Code, staleRename.Body.String())
	staleDelete := api.Delete("/files/32?revision=999", headers...)
	require.Equal(t, http.StatusConflict, staleDelete.Code, staleDelete.Body.String())
}

func TestFileHTTPRestoreAcceptsAReplacementName(t *testing.T) {
	h := newOperationServiceHarness(t)
	node := treeNode(t, h.db, h.tree.prefix, 11)
	_, err := h.service.trashNode(h.ctx, node.ID, node.Version)
	require.NoError(t, err)
	trashedRevision := treeNode(t, h.db, h.tree.prefix, node.ID).Version
	api := newFileHTTPAPI(t, h.service)

	restored := api.Patch("/files/11", "Authorization: Bearer test", map[string]any{
		"state": "active", "name": "Recovered", "revision": trashedRevision,
	})
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	var envelope respx.Envelope[fileEntryView]
	require.NoError(t, json.Unmarshal(restored.Body.Bytes(), &envelope))
	require.Equal(t, "Recovered", envelope.Data.Name)
	require.Equal(t, "active", treeNode(t, h.db, h.tree.prefix, 11).State)
}

func TestFileHTTPTenantAndDepartmentCollectionRoutes(t *testing.T) {
	t.Run("tenant create", func(t *testing.T) {
		h := newOperationServiceHarness(t)
		api := newFileHTTPAPI(t, h.service)
		response := postFileFormTo(t, api, "/tenants/90/files", []any{"Authorization: Bearer test"}, map[string]string{
			"parent_id": "2", "kind": "directory", "name": "Tenant contracts",
		}, "", "")
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		var envelope respx.Envelope[fileEntryView]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		require.Equal(t, "Tenant contracts", envelope.Data.Name)
	})

	t.Run("department list and create", func(t *testing.T) {
		h := newOperationServiceHarness(t)
		q := query.Bind(h.db, h.tree.prefix)
		_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(
			q.Node.RootScope.Value("department"), q.Node.RootOwnerID.Value(guid.ID(80)),
		)
		require.NoError(t, err)
		api := newFileHTTPAPI(t, h.service)
		headers := []any{"Authorization: Bearer test"}

		listed := api.Get("/departments/80/files", headers...)
		require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
		var listedEnvelope respx.Envelope[filePage]
		require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listedEnvelope))
		require.Len(t, listedEnvelope.Data.Items, 1)
		require.Equal(t, "department", listedEnvelope.Data.Items[0].Scope)
		requireOptionalID(t, listedEnvelope.Data.Items[0].DepartmentID, 80)

		created := postFileFormTo(t, api, "/departments/80/files", headers, map[string]string{
			"parent_id": "2", "kind": "directory", "name": "Department contracts",
		}, "", "")
		require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
		var createdEnvelope respx.Envelope[fileEntryView]
		require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdEnvelope))
		require.Equal(t, "Department contracts", createdEnvelope.Data.Name)
	})
}

func postFileFormTo(t *testing.T, api humatest.TestAPI, path string, headers []any, fields map[string]string, filename, content string) *httptest.ResponseRecorder {
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
	return api.Post(path, args...)
}
