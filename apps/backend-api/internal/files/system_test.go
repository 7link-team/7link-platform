package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemSharedFilesLifecycleIsolationAndLiveRevocation(t *testing.T) {
	h := newOperationServiceHarness(t)
	granted := true
	h.service.systemPolicy = func(ctx context.Context, tx *gorm.DB, permission string, principal guid.ID) (bool, error) {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(70), principal)
		require.True(t, strings.HasPrefix(permission, "system_file_"))
		return granted, ctx.Err()
	}
	ctx := systemFileContext(h.ownCtx)
	root, err := h.service.ensureSystemRoot(ctx)
	require.NoError(t, err)
	require.Equal(t, ownerSystem, root.Scope)
	require.Nil(t, root.PrincipalID)
	require.Nil(t, root.TenantID)
	again, err := h.service.ensureSystemRoot(ctx)
	require.NoError(t, err)
	require.Equal(t, root.ID, again.ID)
	page, err := h.service.listRoots(ctx, fileCollection{scope: ownerSystem}, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	children, err := h.service.listChildren(ctx, root.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Equal(t, []filePathView{{ID: root.ID, Name: "System shared files"}}, children.Path)
	file, err := h.service.uploadFile(ctx, root.ID, "shared.txt", "text/plain", strings.NewReader("shared"), 6)
	require.NoError(t, err)
	_, err = h.service.getFileEntry(h.ownCtx, file.ID)
	require.Error(t, err)
	_, err = h.service.getFileEntry(h.ctx, file.ID)
	require.Error(t, err)
	api := newFileHTTPAPI(t, h.service)
	response := api.Get("/files/"+file.ID.String(), "Authorization: Bearer session")
	require.Equal(t, 200, response.Code, response.Body.String())
	trashed, err := h.service.trashNode(ctx, file.ID, file.Revision)
	require.NoError(t, err)
	require.Positive(t, trashed.BatchID)
	page, err = h.service.listTrash(ctx, fileCollection{scope: ownerSystem}, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, []filePathView{{ID: root.ID, Name: "System shared files"}}, page.Items[0].RestorePath)
	_, err = h.service.restoreNode(ctx, file.ID, page.Items[0].Revision)
	require.NoError(t, err)
	granted = false
	_, err = h.service.getFileEntry(ctx, file.ID)
	require.ErrorIs(t, err, errOperationForbidden)
}

func TestSystemFileUnresolvedDeniesFollowActionAndInheritance(t *testing.T) {
	slot := 1
	actor := aclSubject{kind: "principal", id: 70}
	a := &nodeAuthorizer{actorID: 70, acl: map[aclSubject]struct{}{actor: {}}, system: func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return true, nil }}
	for _, tc := range []struct {
		name, kind, action, applies string
		stop, allowed               bool
	}{
		{"role-read-deny", "role", "read", "descendants", false, false},
		{"department-read-deny", "department", "read", "descendants", false, false},
		{"group-read-deny", "group", "read", "descendants", false, false},
		{"other-action", "role", "download", "descendants", false, true},
		{"ancestor-self", "role", "read", "self", false, true},
		{"inheritance-stopped", "role", "read", "descendants", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			permissions := storedNodePermissions{space: &model.Space{ScopeKind: spaceSystem, SystemSlot: &slot}, root: rootBinding{scope: ownerSystem}, managementMode: "user", layers: []nodeACLLayer{
				{nodeID: 1, rules: []nodeACLRule{aclFixtureRule(aclSubject{kind: tc.kind, id: 123}, "deny", tc.applies, tc.action)}},
				{nodeID: 2, stopInherit: tc.stop},
			}}
			allowed, err := a.authorizeLoaded(t.Context(), &gorm.DB{}, permissions, authz.DataConstraint{}, authz.DataConstraint{}, "read")
			require.NoError(t, err)
			require.Equal(t, tc.allowed, allowed)
		})
	}
}

// grantSystemFiles installs a platform policy that answers system_file_*
// inside the caller's transaction from a switch the test controls.
func grantSystemFiles(t *testing.T, h operationServiceHarness) *bool {
	t.Helper()
	granted := true
	h.service.systemPolicy = func(ctx context.Context, tx *gorm.DB, permission string, principal guid.ID) (bool, error) {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(70), principal)
		require.True(t, strings.HasPrefix(permission, "system_file_"), permission)
		return granted, ctx.Err()
	}
	return &granted
}

func decodeSystemFile[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope respx.Envelope[T]
	require.NoError(t, json.Unmarshal(body, &envelope), string(body))
	return envelope.Data
}

func systemFileOperation(destination string, sources ...string) map[string]any {
	return map[string]any{
		"action": "copy", "destination_id": destination, "sources": sources,
		"file_policy": "rename", "directory_policy": "rename", "type_policy": "rename",
	}
}

func TestSystemFileHTTPRoutesManageTheSharedRoot(t *testing.T) {
	h := newOperationServiceHarness(t)
	grantSystemFiles(t, h)
	api := newFileHTTPAPI(t, h.service)
	auth := "Authorization: Bearer session"

	empty := api.Get("/system/files", auth)
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	require.Empty(t, decodeSystemFile[filePage](t, empty.Body.Bytes()).Items)

	rootForm := map[string]string{"kind": "directory", "scope": "system"}
	created := postFileFormTo(t, api, "/system/files", []any{auth}, rootForm, "", "")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	root := decodeSystemFile[fileEntryView](t, created.Body.Bytes())
	require.Equal(t, ownerSystem, root.Scope)
	require.Equal(t, "directory", root.Kind)
	require.Equal(t, "System shared files", root.Name)
	require.Nil(t, root.PrincipalID)
	require.Nil(t, root.TenantID)
	again := postFileFormTo(t, api, "/system/files", []any{auth}, rootForm, "", "")
	require.Equal(t, http.StatusCreated, again.Code, again.Body.String())
	require.Equal(t, root.ID, decodeSystemFile[fileEntryView](t, again.Body.Bytes()).ID, "the system root is one slot, not one per request")

	listed := api.Get("/system/files?limit=1", auth)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	page := decodeSystemFile[filePage](t, listed.Body.Bytes())
	require.Len(t, page.Items, 1)
	require.Equal(t, root.ID, page.Items[0].ID)
	require.Nil(t, page.NextAfter, "the single root fits one page")
	after := api.Get("/system/files?after="+root.ID.String(), auth)
	require.Equal(t, http.StatusOK, after.Code, after.Body.String())
	require.Empty(t, decodeSystemFile[filePage](t, after.Body.Bytes()).Items)

	directory := postFileFormTo(t, api, "/system/files", []any{auth}, map[string]string{"parent_id": root.ID.String(), "kind": "directory", "name": "Archive"}, "", "")
	require.Equal(t, http.StatusCreated, directory.Code, directory.Body.String())
	archive := decodeSystemFile[fileEntryView](t, directory.Body.Bytes())
	uploaded := postFileFormTo(t, api, "/system/files", []any{auth}, map[string]string{"parent_id": root.ID.String(), "kind": "file"}, "notice.txt", "shared")
	require.Equal(t, http.StatusCreated, uploaded.Code, uploaded.Body.String())
	notice := decodeSystemFile[fileEntryView](t, uploaded.Body.Bytes())
	require.Equal(t, "notice.txt", notice.Name)
	require.Equal(t, "file", notice.Kind)
	require.Nil(t, notice.TenantID, "a system file belongs to no tenant")

	copied := api.Post("/system/file-operations", auth, "Idempotency-Key: system-copy", systemFileOperation(archive.ID.String(), notice.ID.String()))
	require.Equal(t, http.StatusCreated, copied.Code, copied.Body.String())
	result := decodeSystemFile[operationResult](t, copied.Body.Bytes())
	require.Equal(t, 1, result.Created)
	require.Len(t, result.Roots, 1)
	children, err := h.service.listChildren(systemFileContext(h.ownCtx), archive.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, children.Items, 1)
	require.Equal(t, "notice.txt", children.Items[0].Name)

	// A root request that is not exactly "the system root" is not quietly
	// turned into one, and system routes never take a scope on a child.
	for name, form := range map[string]map[string]string{
		"account root":       {"kind": "directory", "scope": "account"},
		"named root":         {"kind": "directory", "scope": "system", "name": "Other"},
		"scoped child":       {"parent_id": root.ID.String(), "kind": "directory", "scope": "system", "name": "X"},
		"unscoped file root": {"kind": "file"},
	} {
		refused := postFileFormTo(t, api, "/system/files", []any{auth}, form, "", "")
		require.Equal(t, http.StatusUnprocessableEntity, refused.Code, name+": "+refused.Body.String())
	}
	invalid := api.Post("/system/file-operations", auth, "Idempotency-Key: system-invalid", systemFileOperation("not-an-id", notice.ID.String()))
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code, invalid.Body.String())
	children, err = h.service.listChildren(systemFileContext(h.ownCtx), root.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, children.Items, 2, "refused requests created nothing")
}

func TestSystemFileHTTPRefusesRevokedPermissionAndOtherSpaces(t *testing.T) {
	h := newOperationServiceHarness(t)
	granted := grantSystemFiles(t, h)
	api := newFileHTTPAPI(t, h.service)
	auth := "Authorization: Bearer session"
	systemCtx := systemFileContext(h.ownCtx)
	root, err := h.service.ensureSystemRoot(systemCtx)
	require.NoError(t, err)
	file, err := h.service.uploadFile(systemCtx, root.ID, "keep.txt", "text/plain", strings.NewReader("keep"), 4)
	require.NoError(t, err)

	// A system operation cannot reach into a tenant space in either direction,
	// and the refusal does not disclose that the node exists:
	// node 2 is tenant 90's root and node 32 a file under it.
	intoTenant := api.Post("/system/file-operations", auth, "Idempotency-Key: into-tenant", systemFileOperation("2", file.ID.String()))
	require.Equal(t, http.StatusNotFound, intoTenant.Code, intoTenant.Body.String())
	fromTenant := api.Post("/system/file-operations", auth, "Idempotency-Key: from-tenant", systemFileOperation(root.ID.String(), "32"))
	require.Equal(t, http.StatusNotFound, fromTenant.Code, fromTenant.Body.String())
	// And the caller's own-space route cannot reach into the system space.
	fromAccount := api.Post("/principals/70/file-operations", auth, "Idempotency-Key: from-account", systemFileOperation(root.ID.String(), file.ID.String()))
	require.Equal(t, http.StatusNotFound, fromAccount.Code, fromAccount.Body.String())

	*granted = false
	listed := api.Get("/system/files", auth)
	require.Equal(t, http.StatusForbidden, listed.Code, listed.Body.String())
	rootCreate := postFileFormTo(t, api, "/system/files", []any{auth}, map[string]string{"kind": "directory", "scope": "system"}, "", "")
	require.Equal(t, http.StatusForbidden, rootCreate.Code, rootCreate.Body.String())
	upload := postFileFormTo(t, api, "/system/files", []any{auth}, map[string]string{"parent_id": root.ID.String(), "kind": "file"}, "denied.txt", "x")
	require.Equal(t, http.StatusForbidden, upload.Code, upload.Body.String())
	copied := api.Post("/system/file-operations", auth, "Idempotency-Key: revoked", systemFileOperation(root.ID.String(), file.ID.String()))
	require.Equal(t, http.StatusForbidden, copied.Code, copied.Body.String())

	*granted = true
	children, err := h.service.listChildren(systemCtx, root.ID, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, children.Items, 1, "no refused write left anything behind")
	require.Equal(t, file.ID, children.Items[0].ID)
	tenantChildren, err := h.service.listChildren(h.ctx, 2, browserPageRequest{Limit: 100})
	require.NoError(t, err)
	for _, item := range tenantChildren.Items {
		require.NotEqual(t, "keep.txt", item.Name, "the tenant space received no system file")
	}
}

// systemFileDeniedAuthorizer holds every platform grant except system files.
type systemFileDeniedAuthorizer struct{ fileHTTPAuthorizer }

func (systemFileDeniedAuthorizer) CheckPlatform(_ context.Context, permission string, _ guid.ID) (bool, error) {
	return !strings.HasPrefix(permission, "system_file_"), nil
}

func TestSystemFileHTTPRoutesRequireSystemFileGrants(t *testing.T) {
	h := newOperationServiceHarness(t)
	grantSystemFiles(t, h)
	registry := authz.MustRegistry()
	for _, action := range fileAuthorizationActions() {
		require.NoError(t, registry.Register(action))
	}
	registrar := authz.NewRegistrar(registry, fileHTTPVerifier{}, systemFileDeniedAuthorizer{}, fileHTTPMembership{})
	router := humachi.New(chi.NewRouter(), huma.DefaultConfig("System files test", "1"))
	registerSystemFileHTTP(router, h.service, registrar)
	api := humatest.Wrap(t, router)
	auth := "Authorization: Bearer session"

	paths := router.OpenAPI().Paths
	require.Equal(t, "system_file_view", paths["/system/files"].Get.Extensions[authz.GuardExtensionKey])
	require.Equal(t, "system_file_create", paths["/system/files"].Post.Extensions[authz.GuardExtensionKey])
	require.Equal(t, "system_file_create", paths["/system/file-operations"].Post.Extensions[authz.GuardExtensionKey])

	listed := api.Get("/system/files", auth)
	require.Equal(t, http.StatusForbidden, listed.Code, listed.Body.String())
	created := postFileFormTo(t, api, "/system/files", []any{auth}, map[string]string{"kind": "directory", "scope": "system"}, "", "")
	require.Equal(t, http.StatusForbidden, created.Code, created.Body.String())
	copied := api.Post("/system/file-operations", auth, "Idempotency-Key: denied", systemFileOperation("2", "32"))
	require.Equal(t, http.StatusForbidden, copied.Code, copied.Body.String())

	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceSystem)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "a refused root request provisions no system space")
}

func TestEnsureSystemRootRefusesCallersOutsideTheSystemScope(t *testing.T) {
	h := newOperationServiceHarness(t)
	grantSystemFiles(t, h)
	systemCtx := systemFileContext(h.ownCtx)
	q := query.Bind(h.db, h.tree.prefix)
	systemSpaces := func() int64 {
		count, err := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceSystem)).Count()
		require.NoError(t, err)
		return count
	}

	_, err := h.service.ensureSystemRoot(t.Context())
	require.Error(t, err, "an unauthenticated caller provisions nothing")
	_, err = h.service.ensureSystemRoot(h.ownCtx)
	require.ErrorIs(t, err, errOperationForbidden, "the account scope cannot provision the system root")

	// Viewing system files is not creating them: a view-only operator
	// cannot bring the shared root into existence.
	h.service.systemPolicy = func(_ context.Context, _ *gorm.DB, permission string, _ guid.ID) (bool, error) {
		return permission == "system_file_view", nil
	}
	_, err = h.service.ensureSystemRoot(systemCtx)
	require.ErrorIs(t, err, errOperationForbidden)

	unreadable := errors.New("platform grants unreadable")
	h.service.systemPolicy = func(_ context.Context, _ *gorm.DB, permission string, _ guid.ID) (bool, error) {
		if permission == "system_file_create" {
			return false, unreadable
		}
		return true, nil
	}
	_, err = h.service.ensureSystemRoot(systemCtx)
	require.ErrorIs(t, err, unreadable, "an unreadable grant fails closed")
	require.Zero(t, systemSpaces(), "no refused request provisioned a system space")
}

// TestEnsureSystemRootPropagatesEveryStatementFailure fails the Nth statement
// against each table while the system root is provisioned and again once it
// exists. A reached failure must surface and must leave no partial root behind.
func TestEnsureSystemRootPropagatesEveryStatementFailure(t *testing.T) {
	tables := []string{"file_nodes", "file_node_closure", "file_node_acl", "file_spaces", "file_resource_bindings"}
	for _, existing := range []bool{false, true} {
		reached := 0
		for _, table := range tables {
			for skip := int64(0); skip < 8; skip++ {
				t.Run(fmt.Sprintf("existing=%t/%s/skip%d", existing, table, skip), func(t *testing.T) {
					h := newOperationServiceHarness(t)
					grantSystemFiles(t, h)
					systemCtx := systemFileContext(h.ownCtx)
					if existing {
						_, err := h.service.ensureSystemRoot(systemCtx)
						require.NoError(t, err)
					}
					fault := failTableAfter(t, h.service.runtime.Database().Write(), table, skip)
					_, err := h.service.ensureSystemRoot(systemCtx)
					if !fault.Fired() {
						t.Skipf("ensureSystemRoot never made %d statements against %s", skip+1, table)
					}
					reached++
					require.Error(t, err)
					if !existing {
						q := query.Bind(h.db, h.tree.prefix)
						count, countErr := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceSystem)).Count()
						require.NoError(t, countErr)
						require.Zero(t, count, "a failed provisioning left a partial system space")
					}
				})
			}
		}
		require.NotZero(t, reached, "existing=%t reached no injected failure", existing)
	}
}

func TestEnsureSystemRootFailsWholeWhenIDsRunOut(t *testing.T) {
	for remaining := int64(0); remaining < 2; remaining++ {
		h := newOperationServiceHarness(t)
		grantSystemFiles(t, h)
		ids := &failingIDs{}
		ids.remaining.Store(remaining)
		ids.next.Store(20000)
		h.service.ids = ids
		_, err := h.service.ensureSystemRoot(systemFileContext(h.ownCtx))
		require.ErrorIs(t, err, errOperationInput, "remaining=%d", remaining)
		q := query.Bind(h.db, h.tree.prefix)
		count, err := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceSystem)).Count()
		require.NoError(t, err)
		require.Zero(t, count, "remaining=%d left a system space without its root", remaining)
	}
}

func TestEnsureSystemRootIsNotCreatedWithoutItsAuditRecord(t *testing.T) {
	h := newOperationServiceHarness(t)
	grantSystemFiles(t, h)
	recordFailure := errors.New("audit unavailable")
	h.service.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return recordFailure }
	_, err := h.service.ensureSystemRoot(systemFileContext(h.ownCtx))
	require.ErrorIs(t, err, recordFailure)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.Space.WithContext(t.Context()).Where(q.Space.ScopeKind.Eq(spaceSystem)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "an unrecorded system root was rolled back")
}
