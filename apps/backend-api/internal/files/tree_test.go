package files

import (
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDirectoryTreeClosurePathsAndDirectChildren(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now()
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", now))
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 11, 91, "tenant", now))
	docs, err := tree.createDirectory(t.Context(), db, 1, 3, 2, "Documents", now)
	require.NoError(t, err)
	images, err := tree.createDirectory(t.Context(), db, 1, 4, 3, "图片", now)
	require.NoError(t, err)
	_, err = tree.createDirectory(t.Context(), db, 1, 5, 3, "Été", now)
	require.NoError(t, err)
	_, err = tree.createDirectory(t.Context(), db, 1, 6, 3, "e\u0301TE\u0301", now)
	require.Error(t, err, "casefold + NFC collision must be rejected")
	_, err = tree.createDirectory(t.Context(), db, 1, 7, 11, "foreign", now)
	require.Error(t, err)
	_, err = tree.createDirectory(t.Context(), db, 1, 8, 3, "CON.txt", now)
	require.ErrorIs(t, err, ErrName)
	children, err := tree.children(t.Context(), db, 1, 2, "", 100, 0)
	require.NoError(t, err)
	require.Len(t, children, 1, "must not include grandchildren")
	require.Equal(t, docs.ID, children[0].ID)
	resolved, err := tree.resolvePath(t.Context(), db, 1, "/DOCUMENTS/图片/")
	require.NoError(t, err)
	require.Equal(t, images.ID, resolved.ID)
	_, err = tree.resolvePath(t.Context(), db, 10, "/Documents/图片")
	require.Error(t, err, "a path cannot escape its space")
	for _, path := range []string{"/Documents/../图片", "//Documents", "/Documents//图片", "relative/path"} {
		_, err := tree.resolvePath(t.Context(), db, 1, path)
		require.Error(t, err)
	}
	nodes, err := tree.pathNodes(t.Context(), db, 1, 4)
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	require.Equal(t, []string{"", "Documents", "图片"}, []string{nodes[0].Name, nodes[1].Name, nodes[2].Name})
	q := query.Bind(db, accounts.prefix)
	links, err := q.NodeClosure.WithContext(t.Context()).Where(q.NodeClosure.SpaceID.Eq(guid.ID(1)), q.NodeClosure.DescendantID.Eq(guid.ID(4))).Order(q.NodeClosure.Depth.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, links, 3)
	for depth, link := range links {
		require.Equal(t, depth, link.Depth)
	}
	// Renaming only an ancestor changes derived paths, never descendant rows.
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(3))).UpdateSimple(q.Node.Name.Value("Photos"), q.Node.NameKey.Value("photos"))
	require.NoError(t, err)
	resolved, err = tree.resolvePath(t.Context(), db, 1, "/Photos/图片")
	require.NoError(t, err)
	require.Equal(t, images.ID, resolved.ID)
	_, err = tree.resolvePath(t.Context(), db, 1, "/Documents/图片")
	require.Error(t, err)
}

func TestNodeConstraintsRejectCrossSpaceFileParentsAndInvalidRoots(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now()
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", now))
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 11, 91, "account", now))
	q := query.Bind(db, accounts.prefix)
	slot := 1
	parent := guid.ID(2)
	file := model.Node{ID: 3, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: "a.mp4", NameKey: "a.mp4", Ext: "mp4", State: "active", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	require.NoError(t, q.Node.WithContext(t.Context()).Create(&file))
	for _, variant := range []string{"file_parent", "foreign_parent", "missing_live_slot", "invalid_root", "duplicate_root"} {
		t.Run(variant, func(t *testing.T) {
			node := file
			node.ID = 4
			node.Name, node.NameKey = "child", "child"
			switch variant {
			case "file_parent":
				id := guid.ID(3)
				node.ParentID = &id
			case "foreign_parent":
				id := guid.ID(11)
				node.ParentID = &id
			case "missing_live_slot":
				node.LiveSlot = nil
			case "invalid_root":
				node.ParentID = nil
				node.Kind = "directory"
				node.Name, node.NameKey = "", ""
			case "duplicate_root":
				node.ParentID = nil
				node.RootSlot = &slot
				node.Kind = "directory"
				node.Name, node.NameKey = "", ""
			}
			require.Error(t, q.Node.WithContext(t.Context()).Create(&node))
		})
	}
	require.Error(t, q.NodeClosure.WithContext(t.Context()).Create(&model.NodeClosure{SpaceID: 1, AncestorID: 2, DescendantID: 11, Depth: 1}))
	require.Error(t, q.NodeClosure.WithContext(t.Context()).Create(&model.NodeClosure{SpaceID: 1, AncestorID: 2, DescendantID: 2, Depth: 1}))
	children, err := tree.children(t.Context(), db, 1, 2, "file", 100, 0)
	require.NoError(t, err)
	require.Len(t, children, 1)
	_, err = tree.children(t.Context(), db, 1, 3, "", 100, 0)
	require.Error(t, err)
}

func explainPlan(t *testing.T, db *gorm.DB, sql string, args ...any) string {
	t.Helper()
	var rows []struct{ Detail string }
	require.NoError(t, db.Raw("EXPLAIN QUERY PLAN "+sql, args...).Scan(&rows).Error)
	details := make([]string, 0, len(rows))
	for _, row := range rows {
		details = append(details, row.Detail)
	}
	return strings.Join(details, "\n")
}

func TestDirectoryQueriesUseSQLiteIndexes(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", time.Now()))
	_, err := tree.createDirectory(t.Context(), db, 1, 3, 2, "Documents", time.Now())
	require.NoError(t, err)
	names := query.BindNames(accounts.prefix)
	queries := []struct {
		name, sql, index string
		args             []any
	}{
		{"children", "SELECT id, name, kind FROM " + names.Nodes.String() + " WHERE space_id = ? AND parent_id = ? AND state = ? ORDER BY name_key, id LIMIT 100", "idx_file_nodes_children", []any{1, 2, "active"}},
		{"folders", "SELECT id, name FROM " + names.Nodes.String() + " WHERE space_id = ? AND parent_id = ? AND state = ? AND kind = ? ORDER BY name_key, id LIMIT 100", "idx_file_nodes_children_kind", []any{1, 2, "active", "directory"}},
		{"path_segment", "SELECT id FROM " + names.Nodes.String() + " WHERE space_id = ? AND parent_id = ? AND name_key = ? AND live_slot = 1", "sqlite_autoindex_", []any{1, 2, "documents"}},
		{"ancestors", "SELECT ancestor_id, depth FROM " + names.NodeClosure.String() + " WHERE space_id = ? AND descendant_id = ? ORDER BY depth DESC", "idx_file_node_closure_ancestors", []any{1, 3}},
		{"subtree", "SELECT descendant_id, depth FROM " + names.NodeClosure.String() + " WHERE space_id = ? AND ancestor_id = ? ORDER BY depth, descendant_id", "idx_file_node_closure_descendants", []any{1, 2}},
	}
	for _, item := range queries {
		t.Run(item.name, func(t *testing.T) {
			plan := explainPlan(t, db, item.sql, item.args...)
			t.Log(plan)
			require.Contains(t, plan, "SEARCH ")
			require.Contains(t, plan, item.index)
			require.NotContains(t, plan, "USE TEMP B-TREE")
			require.NotContains(t, plan, "SCAN ")
		})
	}
}

func TestDirectoryDepthLimitAndRollback(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	require.NoError(t, tree.createSpace(context.Background(), db, 1, 2, 90, "tenant", time.Now()))
	parent := guid.ID(2)
	for depth := 1; depth <= maxNodeDepth; depth++ {
		id := guid.ID(depth + 2)
		_, err := tree.createDirectory(t.Context(), db, 1, id, parent, "folder", time.Now())
		require.NoError(t, err)
		parent = id
	}
	_, err := tree.createDirectory(t.Context(), db, 1, 100, parent, "too-deep", time.Now())
	require.ErrorIs(t, err, errHierarchy)
	q := query.Bind(db, accounts.prefix)
	count, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(100))).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	nodes, err := tree.pathNodes(t.Context(), db, 1, parent)
	require.NoError(t, err)
	require.Len(t, nodes, maxNodeDepth+1)
}

func TestDirectoryTreeRejectsInvalidEntryPointInputs(t *testing.T) {
	now := time.Now()
	var nilDB *gorm.DB
	tree := directoryTree{}

	for name, call := range map[string]func() error{
		"create space without database": func() error {
			return tree.createSpace(t.Context(), nilDB, 1, 2, 3, "tenant", now)
		},
		"create space with unsupported scope": func() error {
			return tree.createSpace(t.Context(), &gorm.DB{}, 1, 2, 3, "workspace", now)
		},
		"create directory without parent": func() error {
			_, err := tree.createDirectory(t.Context(), &gorm.DB{}, 1, 2, 0, "reports", now)
			return err
		},
		"list children without page size": func() error {
			_, err := tree.children(t.Context(), &gorm.DB{}, 1, 2, "", 0, 0)
			return err
		},
		"resolve relative path": func() error {
			_, err := tree.resolvePath(t.Context(), &gorm.DB{}, 1, "reports")
			return err
		},
		"load path without node": func() error {
			_, err := tree.pathNodes(t.Context(), &gorm.DB{}, 1, 0)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, call(), errHierarchy)
		})
	}
}

func TestDirectoryTreePropagatesEveryReachedStatementFailure(t *testing.T) {
	type faultCase struct {
		name  string
		table string
		skip  int64
		call  func(*testing.T, *gorm.DB, directoryTree) error
	}
	createNode := func(t *testing.T, db *gorm.DB, tree directoryTree) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := tree.createNodeOn(t.Context(), tx, 1, 100, 11, "directory", "fault-target", time.Now())
			return err
		})
	}
	renameNode := func(t *testing.T, db *gorm.DB, tree directoryTree) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := tree.renameNodeOn(t.Context(), tx, 1, 30, 2, "renamed.txt", time.Now())
			return err
		})
	}
	cases := []faultCase{
		{"create space writes space", "file_spaces", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			return tree.createSpace(t.Context(), db, 100, 101, 92, "tenant", time.Now())
		}},
		{"create space writes root", "file_nodes", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			return tree.createSpace(t.Context(), db, 100, 101, 92, "tenant", time.Now())
		}},
		{"create space writes closure", "file_node_closure", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			return tree.createSpace(t.Context(), db, 100, 101, 92, "tenant", time.Now())
		}},
		{"create system space writes root", "file_nodes", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			return db.Transaction(func(tx *gorm.DB) error {
				return tree.createSystemSpaceOn(t.Context(), tx, 100, 101, time.Now())
			})
		}},
		{"create node locks space", "file_spaces", 0, createNode},
		{"create node reads parent", "file_nodes", 0, createNode},
		{"create node checks name", "file_nodes", 1, createNode},
		{"create node loads ancestors", "file_node_closure", 0, createNode},
		{"create node writes node", "file_nodes", 2, createNode},
		{"create node writes closure", "file_node_closure", 1, createNode},
		{"rename locks space", "file_spaces", 0, renameNode},
		{"rename loads node", "file_nodes", 0, renameNode},
		{"rename checks name", "file_nodes", 1, renameNode},
		{"rename writes node", "file_nodes", 2, renameNode},
		{"children reads parent", "file_nodes", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			_, err := tree.children(t.Context(), db, 1, 10, "", 10, 0)
			return err
		}},
		{"children reads page", "file_nodes", 1, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			_, err := tree.children(t.Context(), db, 1, 10, "", 10, 0)
			return err
		}},
		{"resolve path reads root", "file_nodes", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			_, err := tree.resolvePath(t.Context(), db, 1, "/Source/Shared/Nested/new.txt")
			return err
		}},
		{"resolve path reads segment", "file_nodes", 1, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			_, err := tree.resolvePath(t.Context(), db, 1, "/Source/Shared/Nested/new.txt")
			return err
		}},
		{"load path", "file_nodes", 0, func(t *testing.T, db *gorm.DB, tree directoryTree) error {
			_, err := tree.pathNodes(t.Context(), db, 1, 30)
			return err
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, tree, _ := newOperationTree(t)
			fault := failTableAfter(t, db, testCase.table, testCase.skip)
			err := testCase.call(t, db, tree)
			if !fault.Fired() {
				t.Skip("target statement was not reached")
			}
			require.ErrorIs(t, err, errInjectedTableFault)
		})
	}
}

// TestDirectoryTreeWritePrimitivesRequireValidInputAndACallerTransaction pins
// the contract of the transaction-scoped write primitives: malformed input is
// refused before any statement, and a handle that is not inside the caller's
// transaction is refused so a partial write can never commit on its own.
func TestDirectoryTreeWritePrimitivesRequireValidInputAndACallerTransaction(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	now := time.Now()
	before := treeSpaceRevision(t, db, tree.prefix, 1)
	for name, call := range map[string]func(*gorm.DB) error{
		"create space": func(tx *gorm.DB) error {
			return tree.createSpaceOn(t.Context(), tx, 500, 501, 95, spaceTenant, now)
		},
		"create system space": func(tx *gorm.DB) error {
			return tree.createSystemSpaceOn(t.Context(), tx, 510, 511, now)
		},
		"create node": func(tx *gorm.DB) error {
			_, err := tree.createNodeOn(t.Context(), tx, 1, 520, 2, "directory", "Outside", now)
			return err
		},
		"rename node": func(tx *gorm.DB) error {
			_, err := tree.renameNodeOn(t.Context(), tx, 1, 10, treeNode(t, db, tree.prefix, 10).Version, "Renamed", now)
			return err
		},
	} {
		require.ErrorIs(t, call(db), ErrAccountTransaction, name)
	}
	for name, err := range map[string]error{
		"space without owner":       tree.createSpaceOn(t.Context(), db, 500, 501, 0, spaceTenant, now),
		"space of system scope":     tree.createSpaceOn(t.Context(), db, 500, 501, 95, spaceSystem, now),
		"system space without root": tree.createSystemSpaceOn(t.Context(), db, 510, 0, now),
		"node of unknown kind": func() error {
			_, err := tree.createNodeOn(t.Context(), db, 1, 520, 2, "link", "Outside", now)
			return err
		}(),
		"rename without revision": func() error {
			_, err := tree.renameNodeOn(t.Context(), db, 1, 10, 0, "Renamed", now)
			return err
		}(),
	} {
		require.ErrorIs(t, err, errHierarchy, name)
	}
	require.ErrorIs(t, tree.appendVersion(t.Context(), db, model.Version{ID: 1}, 1, now), errVersionConflict)
	require.Equal(t, before, treeSpaceRevision(t, db, tree.prefix, 1), "no refused primitive touched the space")
	require.Equal(t, "Shared", treeNode(t, db, tree.prefix, 10).Name)
}

func TestDirectoryTreeResolvePathStopsAtFilesAndTheDepthLimit(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	found, err := tree.resolvePath(t.Context(), db, 1, "/Source/Shared/root.txt")
	require.NoError(t, err)
	require.Equal(t, guid.ID(32), found.ID)
	_, err = tree.resolvePath(t.Context(), db, 1, "/Source/Shared/root.txt/inside")
	require.ErrorIs(t, err, ErrNotFound, "a file has no children to walk into")
	_, err = tree.resolvePath(t.Context(), db, 1, "/"+strings.Repeat("d/", maxNodeDepth+1))
	require.ErrorIs(t, err, errHierarchy)
}
