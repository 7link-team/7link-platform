package files

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func testFileCollection(ctx context.Context) fileCollection {
	claims, _ := authn.FromContext(ctx)
	if claims == nil {
		return fileCollection{}
	}
	if claims.TenantID == 0 {
		return fileCollection{scope: "account", ownerID: claims.PrincipalID}
	}
	return fileCollection{scope: "tenant", tenantID: claims.TenantID, ownerID: claims.TenantID}
}

func TestFileBrowserListsAuthorizedRootsPathAndMetadata(t *testing.T) {
	h := newOperationServiceHarness(t)
	roots, err := h.service.listRoots(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, roots.Items, 1)
	require.Equal(t, guid.ID(2), roots.Items[0].ID)
	require.Equal(t, "Tenant files", roots.Items[0].Name)
	require.Equal(t, "directory", roots.Items[0].Kind)
	require.Equal(t, "tenant", roots.Items[0].Scope)
	require.NotNil(t, roots.Items[0].TenantID)
	require.Equal(t, guid.ID(90), *roots.Items[0].TenantID)
	require.Nil(t, roots.Items[0].EntityID)
	require.Nil(t, roots.Items[0].DepartmentID)
	require.Nil(t, roots.Items[0].PrincipalID)
	require.Nil(t, roots.NextAfter)

	first, err := h.service.listChildren(h.ctx, 2, browserPageRequest{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []filePathView{{ID: 2, Name: "Tenant files"}}, first.Path)
	require.Len(t, first.Items, 1)
	require.Equal(t, guid.ID(3), first.Items[0].ID)
	require.NotNil(t, first.NextAfter)
	second, err := h.service.listChildren(h.ctx, 2, browserPageRequest{After: *first.NextAfter, Limit: 1})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, guid.ID(6), second.Items[0].ID)
	require.Nil(t, second.NextAfter)

	page, err := h.service.listChildren(h.ctx, 10, browserPageRequest{})
	require.NoError(t, err)
	require.Equal(t, []filePathView{{ID: 2, Name: "Tenant files"}, {ID: 3, Name: "Source"}, {ID: 10, Name: "Shared"}}, page.Path)
	require.Len(t, page.Items, 2)
	require.Equal(t, guid.ID(11), page.Items[0].ID)
	require.Equal(t, guid.ID(32), page.Items[1].ID)
	require.Equal(t, "file", page.Items[1].Kind)
	require.Equal(t, int64(3), *page.Items[1].Size)
	require.Equal(t, "application/octet-stream", page.Items[1].DetectedType)
	require.EqualValues(t, 1, page.Items[1].Version)
	require.False(t, page.Items[1].UpdatedAt.IsZero())
}

func TestFileRootViewsExposeOrganizationOwnershipWithoutCreatorOwnership(t *testing.T) {
	tenantID, principalID := guid.ID(90), guid.ID(70)
	space := &model.Space{ID: 1, ScopeKind: "tenant", TenantID: &tenantID}
	for _, test := range []struct {
		scope      string
		ownerID    guid.ID
		tenant     guid.ID
		entity     guid.ID
		department guid.ID
		principal  guid.ID
	}{
		{scope: "tenant", ownerID: 90, tenant: 90},
		{scope: "entity", ownerID: 80, tenant: 90, entity: 80},
		{scope: "department", ownerID: 50, tenant: 90, department: 50},
		{scope: "personal", ownerID: 70, tenant: 90, principal: 70},
	} {
		t.Run(test.scope, func(t *testing.T) {
			view := fileEntryView{}
			row := &model.Node{ID: 2, SpaceID: 1, Kind: "directory", State: "active", RootScope: &test.scope, RootOwnerID: &test.ownerID}
			require.NoError(t, setRootOwnership(&view, space, row))
			requireOptionalID(t, view.TenantID, test.tenant)
			requireOptionalID(t, view.EntityID, test.entity)
			requireOptionalID(t, view.DepartmentID, test.department)
			requireOptionalID(t, view.PrincipalID, test.principal)
		})
	}

	accountSpace := &model.Space{ID: 2, ScopeKind: "account", PrincipalID: &principalID}
	accountScope, ownerID := "account", guid.ID(70)
	account := fileEntryView{}
	require.NoError(t, setRootOwnership(&account, accountSpace, &model.Node{
		ID: 3, SpaceID: 2, Kind: "directory", State: "active", RootScope: &accountScope, RootOwnerID: &ownerID,
	}))
	requireOptionalID(t, account.PrincipalID, 70)
	require.Nil(t, account.TenantID)
}

func requireOptionalID(t *testing.T, actual *guid.ID, expected guid.ID) {
	t.Helper()
	if expected == 0 {
		require.Nil(t, actual)
		return
	}
	require.NotNil(t, actual)
	require.Equal(t, expected, *actual)
}

func TestFileBrowserGetsMetadataByIDAndAuthorizedRootRelativePath(t *testing.T) {
	h := newOperationServiceHarness(t)

	byID, err := h.service.getFileEntry(h.ctx, 32)
	require.NoError(t, err)
	require.Equal(t, guid.ID(32), byID.ID)
	require.Equal(t, "root.txt", byID.Name)
	require.Equal(t, "file", byID.Kind)

	byPath, err := h.service.getFileEntryByPath(h.ctx, 2, "Source/Shared/root.txt")
	require.NoError(t, err)
	require.Equal(t, byID, byPath)
	require.NoError(t, query.Bind(h.db, h.tree.prefix).NodeACL.WithContext(t.Context()).Create(&model.NodeACL{
		ID: 9983, SpaceID: 1, NodeID: 10, SubjectKind: "principal", SubjectID: 70,
		Effect: "deny", Action: "traverse", Applies: "self", CreatedBy: 70, CreatedAt: time.Now().UnixMilli(),
	}))
	_, err = h.service.getFileEntryByPath(h.ctx, 2, "Source/Shared/root.txt")
	require.ErrorIs(t, err, errOperationForbidden, "every traversed directory must remain authorized")

	for _, invalid := range []string{"", "/Source", "Source/", "Source//Shared", "Source/../root.txt", `Source\\Shared`} {
		_, err = h.service.getFileEntryByPath(h.ctx, 2, invalid)
		if invalid == "" {
			require.NoError(t, err)
		} else {
			require.Error(t, err, invalid)
		}
	}
	_, err = h.service.getFileEntryByPath(h.ctx, 3, "Shared/root.txt")
	require.ErrorIs(t, err, ErrNotFound, "path roots must be exposed file roots")
}

func TestFileBrowserFiltersDeniedChildrenBeforePagination(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{
		ID: 9980, SpaceID: 1, NodeID: 11, SubjectKind: "principal", SubjectID: 70,
		Effect: "deny", Action: "list", Applies: "self", CreatedBy: 70, CreatedAt: time.Now().UnixMilli(),
	}))
	page, err := h.service.listChildren(h.ctx, 10, browserPageRequest{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, guid.ID(32), page.Items[0].ID)
	require.Nil(t, page.NextAfter, "a filtered row cannot create a false next page")
}

func TestFileBrowserListsOnlyCurrentDeletionAfterRestoreAndRetrash(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	node, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).Take()
	require.NoError(t, err)

	_, err = h.service.trashNode(h.ctx, node.ID, node.Version)
	require.NoError(t, err)
	first, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, first.Items, 1)
	require.Equal(t, node.ID, first.Items[0].ID)
	require.Equal(t, []filePathView{
		{ID: 2, Name: "Tenant files"},
		{ID: 3, Name: "Source"},
		{ID: 10, Name: "Shared"},
	}, first.Items[0].RestorePath)

	restored, err := h.service.restoreNode(h.ctx, node.ID, first.Items[0].Revision)
	require.NoError(t, err)
	_, err = h.service.trashNode(h.ctx, node.ID, restored.Revision)
	require.NoError(t, err)

	second, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.Equal(t, node.ID, second.Items[0].ID)
	count, err := q.TrashRoot.WithContext(t.Context()).Where(q.TrashRoot.NodeID.Eq(node.ID)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count, "historical placements remain available for audit")
}

func TestFileBrowserOmitsRestorePathWhenAnAncestorIsDeniedOrInactive(t *testing.T) {
	t.Run("denied ancestor", func(t *testing.T) {
		h := newOperationServiceHarness(t)
		q := query.Bind(h.db, h.tree.prefix)
		node, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).Take()
		require.NoError(t, err)
		_, err = h.service.trashNode(h.ctx, node.ID, node.Version)
		require.NoError(t, err)
		require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{
			ID: 9984, SpaceID: 1, NodeID: 10, SubjectKind: "principal", SubjectID: 70,
			Effect: "deny", Action: "traverse", Applies: "self", CreatedBy: 70, CreatedAt: time.Now().UnixMilli(),
		}))

		page, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
		require.NoError(t, err)
		require.Len(t, page.Items, 1, "trash read permission remains independent")
		require.Nil(t, page.Items[0].RestorePath, "denied directory names must not leak")
	})

	t.Run("inactive parent", func(t *testing.T) {
		h := newOperationServiceHarness(t)
		q := query.Bind(h.db, h.tree.prefix)
		child, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).Take()
		require.NoError(t, err)
		_, err = h.service.trashNode(h.ctx, child.ID, child.Version)
		require.NoError(t, err)
		parent, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(10))).Take()
		require.NoError(t, err)
		_, err = h.service.trashNode(h.ctx, parent.ID, parent.Version)
		require.NoError(t, err)

		page, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
		require.NoError(t, err)
		byID := make(map[guid.ID]fileEntryView, len(page.Items))
		for _, item := range page.Items {
			byID[item.ID] = item
		}
		require.Contains(t, byID, child.ID)
		require.Nil(t, byID[child.ID].RestorePath, "an inactive original parent makes the display path unavailable")
	})
}

func TestRestoreNameConflictCanRetryWithAReplacementName(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	node, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).Take()
	require.NoError(t, err)
	_, err = h.service.trashNode(h.ctx, node.ID, node.Version)
	require.NoError(t, err)
	page, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	_, err = h.service.createDirectory(h.ctx, 10, "Nested")
	require.NoError(t, err)

	_, err = h.service.restoreNode(h.ctx, node.ID, page.Items[0].Revision)
	require.ErrorIs(t, err, errRestoreNameConflict)
	require.Equal(t, "trashed", treeNode(t, h.db, h.tree.prefix, node.ID).State)
	restored, err := h.service.restoreNodeAs(h.ctx, node.ID, page.Items[0].Revision, "Recovered")
	require.NoError(t, err)
	require.Equal(t, "Recovered", restored.Name)
	require.Equal(t, "active", treeNode(t, h.db, h.tree.prefix, node.ID).State)
}

func TestFileBrowserPageQueriesDoNotGrowWithVisibleFiles(t *testing.T) {
	queryCount := func(t *testing.T, extraFiles int) int64 {
		t.Helper()
		h := newOperationServiceHarness(t)
		for index := 0; index < extraFiles; index++ {
			operationFixtureFile(t, h.db, h.tree, guid.ID(100+index), 10, fmt.Sprintf("page-%03d.txt", index))
		}

		var count atomic.Int64
		database := h.service.runtime.Database().Write()
		callback := "files:count-browser-page-queries"
		require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(*gorm.DB) {
			count.Add(1)
		}))
		t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })

		page, err := h.service.listChildren(h.ctx, 10, browserPageRequest{Limit: maxBrowserPage})
		require.NoError(t, err)
		require.Len(t, page.Items, extraFiles+2)
		return count.Load()
	}

	small := queryCount(t, 0)
	fullPage := queryCount(t, maxBrowserPage-2)
	t.Logf("browser SELECT queries: 2 files=%d, 100 files=%d", small, fullPage)
	require.LessOrEqual(t, fullPage, small+2,
		"a full page must batch paths, ACLs, versions, and objects instead of querying per file")
	require.LessOrEqual(t, fullPage, int64(24), "browser query budget must remain bounded")
}

func TestFileTrashPageRestorePathQueriesDoNotGrowPerEntry(t *testing.T) {
	queryCount := func(t *testing.T, fileCount int) int64 {
		t.Helper()
		h := newOperationServiceHarness(t)
		selected := make([]nodeRevision, 0, fileCount)
		for index := 0; index < fileCount; index++ {
			id := guid.ID(100 + index)
			operationFixtureFile(t, h.db, h.tree, id, 10, fmt.Sprintf("trash-%03d.txt", index))
			selected = append(selected, nodeRevision{ID: id, Version: treeNode(t, h.db, h.tree.prefix, id).Version})
		}
		revision := treeSpaceRevision(t, h.db, h.tree.prefix, 1)
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			return h.tree.trash(t.Context(), tx, 1, 700, 70, revision, selected, 100, time.Now())
		}))

		var count atomic.Int64
		database := h.service.runtime.Database().Write()
		callback := "files:count-trash-page-queries"
		require.NoError(t, database.Callback().Query().Before("gorm:query").Register(callback, func(*gorm.DB) {
			count.Add(1)
		}))
		t.Cleanup(func() { require.NoError(t, database.Callback().Query().Remove(callback)) })

		page, err := h.service.listTrash(h.ctx, testFileCollection(h.ctx), browserPageRequest{Limit: maxBrowserPage})
		require.NoError(t, err)
		require.Len(t, page.Items, fileCount)
		for _, item := range page.Items {
			require.Len(t, item.RestorePath, 3)
		}
		return count.Load()
	}

	small := queryCount(t, 1)
	fullPage := queryCount(t, maxBrowserPage)
	t.Logf("trash browser SELECT queries: 1 file=%d, 100 files=%d", small, fullPage)
	require.LessOrEqual(t, fullPage, small+2, "restore paths must be loaded per page, not per entry")
	require.LessOrEqual(t, fullPage, int64(30), "trash browser query budget must remain bounded")
}

func TestFileBrowserManagementReadBypassesOnlyReadACL(t *testing.T) {
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(
		&model.NodeACL{ID: 9981, SpaceID: 1, NodeID: 2, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "traverse", Applies: "self_and_descendants", CreatedBy: 70, CreatedAt: time.Now().UnixMilli()},
		&model.NodeACL{ID: 9982, SpaceID: 1, NodeID: 2, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "list", Applies: "self_and_descendants", CreatedBy: 70, CreatedAt: time.Now().UnixMilli()},
	))
	ordinary, err := h.service.listRoots(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Empty(t, ordinary.Items)
	h.service.policy = func(ctx context.Context, tx *gorm.DB, tenant, entity guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if tenant != 90 || entity != 80 || actor != 70 {
			return authz.DataConstraint{}, errOperationForbidden
		}
		if permission == fileReadAllPermission || permission == fileViewPermission || permission == "file_list" || permission == "file_traverse" {
			return authz.DataConstraint{AllowAll: true, Revision: 1}, ctx.Err()
		}
		return authz.DataConstraint{Revision: 1}, ctx.Err()
	}
	managed, err := h.service.listRoots(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, managed.Items, 1)
	children, err := h.service.listChildren(h.ctx, 2, browserPageRequest{})
	require.NoError(t, err)
	require.Len(t, children.Items, 2)
}

func TestFileBrowserRejectsInvalidRequestsAndUnavailableService(t *testing.T) {
	h := newOperationServiceHarness(t)
	for _, request := range []browserPageRequest{{After: -1}, {Limit: -1}, {Limit: maxBrowserPage + 1}} {
		_, err := h.service.listRoots(h.ctx, testFileCollection(h.ctx), request)
		require.ErrorIs(t, err, errOperationInput)
	}
	_, err := h.service.listChildren(h.ctx, 0, browserPageRequest{})
	require.ErrorIs(t, err, errOperationInput)
	_, err = h.service.listRoots(t.Context(), testFileCollection(t.Context()), browserPageRequest{})
	require.ErrorIs(t, err, errOperationForbidden)
	h.service.references = nil
	_, err = h.service.listRoots(h.ctx, testFileCollection(h.ctx), browserPageRequest{})
	require.ErrorIs(t, err, errOperationUnavailable)
}
