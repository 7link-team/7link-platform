package files

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
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
