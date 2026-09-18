package files

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func moveTreeNode(t *testing.T, db *gorm.DB, tree directoryTree, sourceID, destinationID guid.ID, name string, nodes, links int) error {
	t.Helper()
	source := treeNode(t, db, tree.prefix, sourceID)
	destination := treeNode(t, db, tree.prefix, destinationID)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	return db.Transaction(func(tx *gorm.DB) error {
		return tree.move(t.Context(), tx, 1, nodeRevision{source.ID, source.Version}, nodeRevision{destination.ID, destination.Version}, revision, name, nodes, links, time.Now())
	})
}

func TestMoveClosurePathsPreserveInternalStructureAndOldTrash(t *testing.T) {
	db, tree := newTrashTree(t)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 5))
	before := *treeNode(t, db, tree.prefix, 4)
	require.NoError(t, moveTreeNode(t, db, tree, 3, 6, "Moved", 3, 9))
	row := treeNode(t, db, tree.prefix, 3)
	require.Equal(t, guid.ID(6), *row.ParentID)
	require.EqualValues(t, 2, row.Version)
	require.Equal(t, before, *treeNode(t, db, tree.prefix, 4), "descendant row/name/version stays unchanged")
	_, err := tree.resolvePath(t.Context(), db, 1, "/Documents/Images")
	require.Error(t, err)
	resolved, err := tree.resolvePath(t.Context(), db, 1, "/Other/Moved/Images")
	require.NoError(t, err)
	require.Equal(t, guid.ID(4), resolved.ID)
	q := query.Bind(db, tree.prefix)
	closure := q.NodeClosure
	links, err := closure.WithContext(t.Context()).Where(closure.SpaceID.Eq(guid.ID(1)), closure.DescendantID.Eq(guid.ID(5))).Order(closure.Depth.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, links, 5, "trash descendant gets new ancestry but stays hidden")
	require.Equal(t, []guid.ID{5, 4, 3, 6, 2}, []guid.ID{links[0].AncestorID, links[1].AncestorID, links[2].AncestorID, links[3].AncestorID, links[4].AncestorID})
	for i, link := range links {
		require.Equal(t, i, link.Depth)
	}
	require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 5).State)
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
	resolved, err = tree.resolvePath(t.Context(), db, 1, "/Other/Moved/Images/Nested")
	require.NoError(t, err)
	require.Equal(t, guid.ID(5), resolved.ID)
	require.NoError(t, moveTreeNode(t, db, tree, 3, 6, "RENAMED", 1, 1))
	_, err = tree.resolvePath(t.Context(), db, 1, "/Other/renamed/Images/Nested")
	require.NoError(t, err)
	linksAfter, err := closure.WithContext(t.Context()).Where(closure.SpaceID.Eq(guid.ID(1)), closure.DescendantID.Eq(guid.ID(5))).Order(closure.Depth.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, links, linksAfter, "same-parent rename does not rewrite closure")
}

func TestMoveRejectsCyclesBudgetsConflictsAndPinnedVersions(t *testing.T) {
	db, tree := newTrashTree(t)
	q := query.Bind(db, tree.prefix)
	before, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	for _, invalid := range []struct {
		source, target guid.ID
		name           string
		nodes, links   int
	}{{3, 3, "Self", 10, 100}, {3, 5, "Cycle", 10, 100}, {2, 6, "Root", 10, 100}, {3, 6, "TooManyNodes", 2, 100}, {3, 6, "TooManyLinks", 3, 8}, {3, 2, "OTHER", 10, 100}, {3, 6, "CON", 10, 100}} {
		require.Error(t, moveTreeNode(t, db, tree, invalid.source, invalid.target, invalid.name, invalid.nodes, invalid.links))
		require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	}
	for _, invalid := range []struct {
		space    guid.ID
		source   nodeRevision
		target   nodeRevision
		revision int64
	}{{1, nodeRevision{3, 2}, nodeRevision{6, 1}, revision}, {1, nodeRevision{3, 1}, nodeRevision{6, 2}, revision}, {1, nodeRevision{3, 1}, nodeRevision{6, 1}, revision - 1}, {99, nodeRevision{3, 1}, nodeRevision{6, 1}, 1}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			return tree.move(t.Context(), tx, invalid.space, invalid.source, invalid.target, invalid.revision, "Moved", 10, 100, time.Now())
		}))
	}
	require.ErrorIs(t, tree.move(t.Context(), db, 1, nodeRevision{3, 1}, nodeRevision{6, 1}, revision, "Moved", 10, 100, time.Now()), ErrAccountTransaction)
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 11, 91, "tenant", time.Now()))
	require.Error(t, moveTreeNode(t, db, tree, 3, 11, "Foreign", 10, 100))
	after, err := q.NodeClosure.WithContext(t.Context()).Where(q.NodeClosure.SpaceID.Eq(guid.ID(1))).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 6))
	require.Error(t, moveTreeNode(t, db, tree, 3, 6, "HiddenTarget", 10, 100))
	require.Error(t, moveTreeNode(t, db, tree, 6, 3, "HiddenSource", 10, 100))
}

func TestMoveLateNodeUpdateFailureRollsBackClosure(t *testing.T) {
	db, tree := newTrashTree(t)
	q := query.Bind(db, tree.prefix)
	before, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	names := query.BindNames(tree.prefix)
	require.NoError(t, db.Exec("CREATE TRIGGER fail_move_node BEFORE UPDATE ON "+names.Nodes.String()+" WHEN NEW.id = 3 AND NEW.parent_id = 6 BEGIN SELECT RAISE(ABORT, 'injected node failure'); END").Error)
	require.ErrorContains(t, moveTreeNode(t, db, tree, 3, 6, "Moved", 10, 100), "injected node failure")
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	after, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, guid.ID(2), *treeNode(t, db, tree.prefix, 3).ParentID)
	require.NoError(t, db.Exec("DROP TRIGGER fail_move_node").Error)
	rollback := errors.New("audit failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tree.move(t.Context(), tx, 1, nodeRevision{3, 1}, nodeRevision{6, 1}, revision, "Moved", 10, 100, time.Now()))
		return rollback
	}), rollback)
	require.Equal(t, guid.ID(2), *treeNode(t, db, tree.prefix, 3).ParentID)
	links, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, before, links)
}

func TestMoveMaximumDepthIncludesTrashedDescendants(t *testing.T) {
	db, tree := newTrashTree(t)
	parent := guid.ID(6)
	for depth := 2; depth <= maxNodeDepth; depth++ {
		id := guid.ID(1000 + depth)
		_, err := tree.createDirectory(t.Context(), db, 1, id, parent, fmt.Sprintf("Level-%d", depth), time.Now())
		require.NoError(t, err)
		parent = id
	}
	require.NoError(t, trashSelection(t, db, tree, 100, 10, 5))
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	require.ErrorIs(t, moveTreeNode(t, db, tree, 3, guid.ID(1000+maxNodeDepth-2), "OverDepth", 10, 1000), errHierarchy)
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	// Destination depth61 + source depth1 + deepest descendant depth2 =64.
	require.NoError(t, moveTreeNode(t, db, tree, 3, guid.ID(1000+maxNodeDepth-3), "AtLimit", 10, 1000))
	require.NoError(t, restoreBatch(t, db, tree, 100, 10))
	path, err := tree.pathNodes(t.Context(), db, 1, 5)
	require.NoError(t, err)
	require.Len(t, path, maxNodeDepth+1)
}

func TestMoveFileRenamePreservesHeadAndMetadata(t *testing.T) {
	db, tree := newTrashTree(t)
	q := query.Bind(db, tree.prefix)
	parent, slot := guid.ID(3), 1
	file := model.Node{ID: 7, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: "movie.mp4", NameKey: "movie.mp4", Ext: "mp4", State: "active", Version: 1}
	require.NoError(t, q.Node.WithContext(t.Context()).Create(&file))
	require.NoError(t, q.NodeClosure.WithContext(t.Context()).Create(&model.NodeClosure{SpaceID: 1, AncestorID: 7, DescendantID: 7, Depth: 0}, &model.NodeClosure{SpaceID: 1, AncestorID: 3, DescendantID: 7, Depth: 1}, &model.NodeClosure{SpaceID: 1, AncestorID: 2, DescendantID: 7, Depth: 2}))
	object := model.Object{ID: 8, BackendID: "local", StorageKey: "objects/8", URI: "file:///backend/objects/8", State: "verified", Size: 3, MIME: "video/mp4", Type: "video", Subtype: "mp4", MetadataJSON: "{}", MetadataState: "unknown", Version: 1}
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&object))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.appendVersion(t.Context(), tx, model.Version{ID: 9, SpaceID: 1, NodeID: 7, ObjectID: 8, Number: 1, CreatedBy: 90, Ext: "mp4"}, 1, time.Now())
	}))
	require.NoError(t, moveTreeNode(t, db, tree, 7, 6, "movie.APK", 1, 4))
	row := treeNode(t, db, tree.prefix, 7)
	require.Equal(t, "apk", row.Ext)
	require.Equal(t, guid.ID(9), *row.CurrentVersionID)
	version, err := q.Version.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "mp4", version.Ext)
	physical, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "video/mp4", physical.MIME)
	require.Error(t, moveTreeNode(t, db, tree, 3, 7, "file-is-not-parent", 10, 100))
}

func TestConcurrentMovesOnlyOnePinnedSpaceRevisionCommits(t *testing.T) {
	db, tree := newTrashTree(t)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, sourceID := range []guid.ID{3, 4} {
		go func(id guid.ID) {
			<-start
			results <- db.Transaction(func(tx *gorm.DB) error {
				return tree.move(t.Context(), tx, 1, nodeRevision{id, 1}, nodeRevision{6, 1}, revision, fmt.Sprintf("Moved-%d", id), 10, 100, time.Now())
			})
		}(sourceID)
	}
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, errTreeConflict)
			conflicted++
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)
	require.Equal(t, revision+1, treeSpaceRevision(t, db, tree.prefix, 1))
	for _, id := range []guid.ID{3, 4, 5, 6} {
		_, err := tree.pathNodes(t.Context(), db, 1, id)
		require.NoError(t, err, "winning move leaves a consistent complete closure path")
	}
}

func TestComposedMovesFailureRollsBackEarlierMove(t *testing.T) {
	db, tree := newTrashTree(t)
	revision := treeSpaceRevision(t, db, tree.prefix, 1)
	q := query.Bind(db, tree.prefix)
	before, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		if err := tree.move(t.Context(), tx, 1, nodeRevision{4, 1}, nodeRevision{6, 1}, revision, "Conflict", 10, 100, time.Now()); err != nil {
			return err
		}
		return tree.move(t.Context(), tx, 1, nodeRevision{3, 1}, nodeRevision{6, 1}, revision+1, "CONFLICT", 10, 100, time.Now())
	}), errTreeConflict)
	require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
	require.Equal(t, guid.ID(3), *treeNode(t, db, tree.prefix, 4).ParentID)
	after, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
	require.NoError(t, err)
	require.Equal(t, before, after)
}
