package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func treeSpaceRevision(t *testing.T, db *gorm.DB, prefix string, spaceID guid.ID) int64 {
	t.Helper()
	q := query.Bind(db, prefix)
	space, err := q.Space.WithContext(t.Context()).Where(q.Space.ID.Eq(spaceID)).Take()
	require.NoError(t, err)
	return space.Version
}

func treeNode(t *testing.T, db *gorm.DB, prefix string, id guid.ID) *model.Node {
	t.Helper()
	q := query.Bind(db, prefix)
	row, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(id)).Take()
	require.NoError(t, err)
	return row
}

func trashSelection(t *testing.T, db *gorm.DB, tree directoryTree, batchID guid.ID, budget int, ids ...guid.ID) error {
	t.Helper()
	selected := make([]nodeRevision, 0, len(ids))
	for _, id := range ids {
		selected = append(selected, nodeRevision{ID: id, Version: treeNode(t, db, tree.prefix, id).Version})
	}
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	return db.Transaction(func(tx *gorm.DB) error {
		return tree.trash(t.Context(), tx, 1, batchID, 90, revision, selected, budget, time.Now())
	})
}

func restoreBatch(t *testing.T, db *gorm.DB, tree directoryTree, batchID guid.ID, budget int) error {
	t.Helper()
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	return db.Transaction(func(tx *gorm.DB) error {
		return tree.restoreOriginal(t.Context(), tx, 1, batchID, 91, revision, budget, time.Now())
	})
}

func newTrashTree(t *testing.T) (*gorm.DB, directoryTree) {
	t.Helper()
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", time.Now()))
	for _, item := range []struct {
		id, parent guid.ID
		name       string
	}{{3, 2, "Documents"}, {4, 3, "Images"}, {5, 4, "Nested"}, {6, 2, "Other"}} {
		_, err := tree.createDirectory(t.Context(), db, 1, item.id, item.parent, item.name, time.Now())
		require.NoError(t, err)
	}
	return db, tree
}

func TestTrashNormalizedRootsReleaseNamesAndRestore(t *testing.T) {
	db, tree := newTrashTree(t)
	q := query.Bind(db, tree.prefix)
	before, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 5, 3, 3, 6))
	batch, err := q.TrashBatch.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, 2, batch.RootCount)
	require.Equal(t, 4, batch.NodeCount)
	for _, id := range []guid.ID{3, 4, 5, 6} {
		row := treeNode(t, db, tree.prefix, id)
		require.Equal(t, "trashed", row.State)
		require.Nil(t, row.LiveSlot)
		require.Equal(t, guid.ID(100), *row.TrashBatchID)
	}
	children, err := tree.children(t.Context(), db, 1, 2, "", 50, 0)
	require.NoError(t, err)
	require.Empty(t, children)
	_, err = tree.children(t.Context(), db, 1, 4, "", 50, 0)
	require.Error(t, err)
	_, err = tree.resolvePath(t.Context(), db, 1, "/Documents/Images/Nested")
	require.Error(t, err)
	_, err = tree.pathNodes(t.Context(), db, 1, 5)
	require.Error(t, err)
	_, err = tree.createDirectory(t.Context(), db, 1, 7, 3, "cannot-create", time.Now())
	require.Error(t, err)
	_, err = tree.createDirectory(t.Context(), db, 1, 7, 2, "DOCUMENTS", time.Now())
	require.NoError(t, err, "deletion releases casefold name slot")
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	require.ErrorIs(t, restoreBatch(t, db, tree, 100, 10), errTreeConflict)
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 6).State, "unconflicted roots must not partly restore")
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(7))).UpdateSimple(q.Node.Name.Value("Replacement"), q.Node.NameKey.Value("replacement"))
	require.NoError(t, err)
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
	for _, id := range []guid.ID{3, 4, 5, 6} {
		row := treeNode(t, db, tree.prefix, id)
		require.Equal(t, "active", row.State)
		require.Nil(t, row.TrashBatchID)
		require.Equal(t, 1, *row.LiveSlot)
		require.EqualValues(t, 3, row.Version)
	}
	batch, err = q.TrashBatch.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "restored", batch.State)
	require.Equal(t, guid.ID(91), *batch.RestoredBy)
	require.NotNil(t, batch.RestoredAt)
	after, err := q.NodeClosure.WithContext(t.Context()).Where(q.NodeClosure.DescendantID.Neq(guid.ID(7))).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, before, after, "delete/restore never rewrites closure")
	resolved, err := tree.resolvePath(t.Context(), db, 1, "/Documents/Images/Nested")
	require.NoError(t, err)
	require.Equal(t, guid.ID(5), resolved.ID)
	require.Error(t, restoreBatch(t, db, tree, 100, 10), "batch cannot be restored twice")
}

func TestTrashNestedOldDeletionDoesNotRevive(t *testing.T) {
	db, tree := newTrashTree(t)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 5))
	require.NoError(t, trashSelection(t, db, tree, 101, 10, 3))
	require.Equal(t, guid.ID(100), *treeNode(t, db, tree.prefix, 5).TrashBatchID)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	require.Error(t, restoreBatch(t, db, tree, 100, 10), "deleted parent is not a valid restore destination")
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	require.NoError(t, restoreBatch(t, db, tree, 101, 10))
	require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 5).State)
	require.Equal(t, guid.ID(100), *treeNode(t, db, tree.prefix, 5).TrashBatchID)
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
	require.Equal(t, "active", treeNode(t, db, tree.prefix, 5).State)
}

func TestTrashBudgetsVersionsScopeAndCallerRollback(t *testing.T) {
	db, tree := newTrashTree(t)
	q := query.Bind(db, tree.prefix)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	for _, invalid := range []struct {
		space    guid.ID
		revision int64
		selected []nodeRevision
		budget   int
	}{
		{1, revision, []nodeRevision{{3, 1}}, 2},
		{1, revision - 1, []nodeRevision{{3, 1}}, 10},
		{1, revision, []nodeRevision{{3, 2}}, 10},
		{1, revision, []nodeRevision{{2, 1}}, 10},
		{1, revision, []nodeRevision{{3, 1}, {3, 2}}, 10},
		{1, revision, []nodeRevision{{3, 1}, {999, 1}}, 10},
		{999, 1, []nodeRevision{{3, 1}}, 10},
	} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			return tree.trash(t.Context(), tx, invalid.space, 100, 90, invalid.revision, invalid.selected, invalid.budget, time.Now())
		}))
		require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	}
	require.ErrorIs(t, tree.trash(t.Context(), db, 1, 100, 90, revision, []nodeRevision{{3, 1}}, 10, time.Now()), ErrAccountTransaction)
	rollback := errors.New("audit failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tree.trash(t.Context(), tx, 1, 100, 90, revision, []nodeRevision{{3, 1}}, 10, time.Now()))
		return rollback
	}), rollback)
	count, err := q.TrashBatch.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, "active", treeNode(t, db, tree.prefix, 3).State)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 3))
	revision = treeSpaceRevision(t, db, tree.prefix, 1)
	require.Error(t, restoreBatch(t, db, tree, 100, 2))
	require.ErrorIs(t, tree.restoreOriginal(t.Context(), db, 1, 100, 90, revision, 10, time.Now()), ErrAccountTransaction)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tree.restoreOriginal(t.Context(), tx, 1, 100, 91, revision, 10, time.Now()))
		return rollback
	}), rollback)
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 3).State)
	batch, err := q.TrashBatch.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "active", batch.State)
	require.Nil(t, batch.RestoredAt)
}

func TestTrashSchemaScopeAndPopulatedMigrationRollback(t *testing.T) {
	db, tree := newTrashTree(t)
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 11, 91, "tenant", time.Now()))
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 3))
	q := query.Bind(db, tree.prefix)
	_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).UpdateSimple(q.Node.TrashBatchID.Value(guid.ID(100)))
	require.Error(t, err, "cross-space batch attachment rejected")
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(6))).UpdateSimple(q.Node.TrashBatchID.Value(guid.ID(100)))
	require.Error(t, err, "active node cannot have trash association")
	require.Error(t, q.TrashRoot.WithContext(t.Context()).Create(&model.TrashRoot{SpaceID: 1, BatchID: 100, NodeID: 6, ParentID: 11, ParentKind: "directory", Name: "Other", NameKey: "other"}))
	require.Error(t, q.TrashRoot.WithContext(t.Context()).Create(&model.TrashRoot{SpaceID: 10, BatchID: 100, NodeID: 11, ParentID: 3, ParentKind: "directory", Name: "Other", NameKey: "other"}))
	plan := explainPlan(t, db, "SELECT id FROM "+query.BindNames(tree.prefix).Nodes.String()+" WHERE space_id = ? AND trash_batch_id = ? ORDER BY id LIMIT 100", 1, 100)
	require.Contains(t, plan, "idx_file_nodes_trash_batch")
	require.Contains(t, plan, "SEARCH ")
	require.NotContains(t, plan, "SCAN ")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = tree.prefix
	require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 5))
	require.False(t, db.Migrator().HasTable(query.BindNames(tree.prefix).TrashBatches.String()))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
}

func TestTrashSecondSQLChunkFailureRollsBackWholeBatch(t *testing.T) {
	db, tree := newTrashTree(t)
	for i := 0; i < 105; i++ {
		_, err := tree.createDirectory(t.Context(), db, 1, guid.ID(1000+i), 3, fmt.Sprintf("Child-%03d", i), time.Now())
		require.NoError(t, err)
	}
	q := query.Bind(db, tree.prefix)
	names := query.BindNames(tree.prefix)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	// ID 1104 is in the second update chunk, after the first chunk succeeded.
	require.NoError(t, db.Exec("CREATE TRIGGER fail_delete_chunk BEFORE UPDATE ON "+names.Nodes.String()+" WHEN NEW.id = 1104 AND NEW.state = 'trashed' BEGIN SELECT RAISE(ABORT, 'injected late delete failure'); END").Error)
	require.ErrorContains(t, trashSelection(t, db, tree, 100, 200, 3), "injected late delete failure")
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	count, err := q.Node.WithContext(t.Context()).Where(q.Node.State.Eq("trashed")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = q.TrashRoot.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = q.TrashBatch.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TRIGGER fail_delete_chunk").Error)
	require.NoError(t, trashSelection(t, db, tree, 100, 200, 3))
	revision = treeSpaceRevision(t, db, tree.prefix, 1)
	require.NoError(t, db.Exec("CREATE TRIGGER fail_restore_chunk BEFORE UPDATE ON "+names.Nodes.String()+" WHEN NEW.id = 1104 AND NEW.state = 'active' BEGIN SELECT RAISE(ABORT, 'injected late restore failure'); END").Error)
	require.ErrorContains(t, restoreBatch(t, db, tree, 100, 200), "injected late restore failure")
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	count, err = q.Node.WithContext(t.Context()).Where(q.Node.TrashBatchID.Eq(guid.ID(100)), q.Node.State.Eq("trashed")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 108, count)
	batch, err := q.TrashBatch.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "active", batch.State)
	require.Nil(t, batch.RestoredAt)
	require.NoError(t, db.Exec("DROP TRIGGER fail_restore_chunk").Error)
	require.NoError(t, restoreBatch(t, db, tree, 100, 200))
}

func TestTrashVersionsProtectStoredContentWithoutAccountReferences(t *testing.T) {
	db, accounts, local := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now()
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", now))
	q := query.Bind(db, tree.prefix)
	parent, slot := guid.ID(2), 1
	file := model.Node{ID: 3, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: "image.png", NameKey: "image.png", Ext: "png", State: "active", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	require.NoError(t, q.Node.WithContext(t.Context()).Create(&file))
	require.NoError(t, q.NodeClosure.WithContext(t.Context()).Create(&model.NodeClosure{SpaceID: 1, AncestorID: 3, DescendantID: 3, Depth: 0}, &model.NodeClosure{SpaceID: 1, AncestorID: 2, DescendantID: 3, Depth: 1}))
	for i, content := range []string{"old", "new"} {
		objectID, refID, versionID := guid.ID(10+i*3), guid.ID(11+i*3), guid.ID(12+i*3)
		key := fmt.Sprintf("objects/%d", objectID)
		prepared, err := accounts.Stage(t.Context(), db, objectID, 90, key, bytes.NewBufferString(content), int64(len(content)), now.Add(time.Hour))
		require.NoError(t, err)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := accounts.Register(t.Context(), tx, prepared, refID, 90, "avatar", nil); err != nil {
				return err
			}
			return tree.appendVersion(t.Context(), tx, model.Version{ID: versionID, SpaceID: 1, NodeID: 3, ObjectID: objectID, Number: int64(i + 1), CreatedBy: 90, Ext: "png"}, int64(i+1), now)
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReleaseAccountResources(t.Context(), tx, 90, now) }))
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 3))
	_, err := accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	for i, content := range []string{"old", "new"} {
		reader, err := local.Open(t.Context(), fmt.Sprintf("objects/%d", 10+i*3))
		require.NoError(t, err)
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, content, string(body))
	}
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
	file = *treeNode(t, db, tree.prefix, 3)
	require.Equal(t, guid.ID(15), *file.CurrentVersionID)
	versions, err := q.Version.WithContext(t.Context()).Order(q.Version.Number.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, guid.ID(10), versions[0].ObjectID)
	require.Equal(t, guid.ID(13), versions[1].ObjectID)
}

func TestTreeMutationValidationAndRestoreSnapshotConsistency(t *testing.T) {
	db, tree := newTrashTree(t)
	now := time.Now()
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	for _, invalid := range []struct {
		batch, actor guid.ID
		selected     []nodeRevision
		budget       int
	}{{0, 90, []nodeRevision{{3, 1}}, 10}, {100, 0, []nodeRevision{{3, 1}}, 10}, {100, 90, nil, 10}, {100, 90, []nodeRevision{{3, 1}}, 0}, {100, 90, []nodeRevision{{3, 1}, {6, 1}}, 1}, {100, 90, []nodeRevision{{0, 1}}, 10}, {100, 90, []nodeRevision{{3, 0}}, 10}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			return tree.trash(t.Context(), tx, 1, invalid.batch, invalid.actor, revision, invalid.selected, invalid.budget, now)
		}), errHierarchy)
	}
	require.ErrorIs(t, tree.trash(t.Context(), nil, 1, 100, 90, revision, []nodeRevision{{3, 1}}, 10, now), errHierarchy)
	require.ErrorIs(t, tree.restoreOriginal(t.Context(), nil, 1, 100, 90, revision, 10, now), errHierarchy)
	require.ErrorIs(t, tree.restoreOriginal(t.Context(), db, 1, 0, 90, revision, 10, now), errHierarchy)
	require.ErrorIs(t, tree.restoreOriginal(t.Context(), db, 1, 100, 0, revision, 10, now), errHierarchy)
	require.ErrorIs(t, tree.restoreOriginal(t.Context(), db, 1, 100, 90, revision, 0, now), errHierarchy)
	require.ErrorIs(t, tree.move(t.Context(), nil, 1, nodeRevision{3, 1}, nodeRevision{6, 1}, revision, "Moved", 10, 100, now), errHierarchy)
	require.ErrorIs(t, tree.move(t.Context(), db, 1, nodeRevision{3, 0}, nodeRevision{6, 1}, revision, "Moved", 10, 100, now), errHierarchy)
	_, err := tree.lockMutation(t.Context(), db, 0, revision)
	require.ErrorIs(t, err, errHierarchy)
	_, err = tree.lockMutation(t.Context(), db, 1, 0)
	require.ErrorIs(t, err, errHierarchy)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 3))
	q := query.Bind(db, tree.prefix)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(3))).UpdateSimple(q.Node.Name.Value("Changed"), q.Node.NameKey.Value("changed"))
	require.NoError(t, err)
	require.ErrorIs(t, restoreBatch(t, db, tree, 100, 10), errTreeConflict)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(3))).UpdateSimple(q.Node.Name.Value("Documents"), q.Node.NameKey.Value("documents"))
	require.NoError(t, err)
	_, err = q.TrashBatch.WithContext(t.Context()).Where(q.TrashBatch.ID.Eq(guid.ID(100))).UpdateSimple(q.TrashBatch.NodeCount.Add(1))
	require.NoError(t, err)
	require.ErrorIs(t, restoreBatch(t, db, tree, 100, 10), errHierarchy)
	_, err = q.TrashBatch.WithContext(t.Context()).Where(q.TrashBatch.ID.Eq(guid.ID(100))).UpdateSimple(q.TrashBatch.NodeCount.Sub(1))
	require.NoError(t, err)
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
}
