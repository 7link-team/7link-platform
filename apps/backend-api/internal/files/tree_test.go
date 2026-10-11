package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
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

func TestEnsureSpaceCreatesOnceAndReportsWhetherItDid(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	ids := &operationServiceIDs{}
	ids.next.Store(700)
	next := func() (guid.ID, error) {
		id, err := ids.Next()
		return guid.ID(id), err
	}
	ensure := func(principalID, tenantID guid.ID) (*model.Space, bool) {
		var space *model.Space
		var created bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			space, created, err = tree.ensureSpaceOn(t.Context(), tx, principalID, tenantID, next, time.Now())
			return err
		}))
		return space, created
	}
	q := query.Bind(db, tree.prefix)
	for _, owner := range []struct {
		name                  string
		principalID, tenantID guid.ID
		scope                 string
		stopInheritance       bool
	}{
		{"account", 70, 0, spaceAccount, false},
		{"tenant", 0, 90, spaceTenant, false},
		{"system", 0, 0, spaceSystem, true},
	} {
		t.Run(owner.name, func(t *testing.T) {
			first, created := ensure(owner.principalID, owner.tenantID)
			require.True(t, created)
			require.Equal(t, owner.scope, first.ScopeKind)
			again, created := ensure(owner.principalID, owner.tenantID)
			require.False(t, created, "an existing space is returned, not created")
			require.Equal(t, first.ID, again.ID)
			roots, err := q.Node.WithContext(t.Context()).Where(q.Node.SpaceID.Eq(first.ID), q.Node.ParentID.IsNull()).Find()
			require.NoError(t, err)
			require.Len(t, roots, 1)
			require.Equal(t, owner.stopInheritance, roots[0].ACLStopInheritance)
		})
	}

	for name, call := range map[string]func(*gorm.DB) error{
		"nil transaction": func(*gorm.DB) error {
			_, _, err := tree.ensureSpaceOn(t.Context(), nil, 70, 0, next, time.Now())
			return err
		},
		"nil identifiers": func(tx *gorm.DB) error {
			_, _, err := tree.ensureSpaceOn(t.Context(), tx, 70, 0, nil, time.Now())
			return err
		},
		"two owners": func(tx *gorm.DB) error {
			_, _, err := tree.ensureSpaceOn(t.Context(), tx, 70, 90, next, time.Now())
			return err
		},
		"negative owner": func(tx *gorm.DB) error {
			_, _, err := tree.ensureSpaceOn(t.Context(), tx, -1, 0, next, time.Now())
			return err
		},
	} {
		require.ErrorIs(t, db.Transaction(call), errHierarchy, name)
	}
	_, _, err := tree.ensureSpaceOn(t.Context(), db, 71, 0, next, time.Now())
	require.ErrorIs(t, err, ErrAccountTransaction, "outside a transaction nothing is written")

	exhausted := errors.New("identifiers exhausted")
	for remaining := range 2 {
		calls := 0
		starved := func() (guid.ID, error) {
			if calls == remaining {
				return 0, exhausted
			}
			calls++
			return next()
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := tree.ensureSpaceOn(t.Context(), tx, 72, 0, starved, time.Now())
			return err
		})
		require.ErrorIs(t, err, exhausted, "remaining=%d", remaining)
		count, err := q.Space.WithContext(t.Context()).Where(q.Space.PrincipalID.Eq(guid.ID(72))).Count()
		require.NoError(t, err)
		require.Zero(t, count, "remaining=%d: a failed initialization leaves no space", remaining)
	}
}

// TestSpaceInitializationConcurrencyExternalDatabase starts the first use of an
// account, a tenant and the system space from concurrent transactions on a
// real database server, where SQLite's whole-database writer lock cannot hide a
// read-then-insert race. The tests/lifecycle PostgreSQL gate supplies the
// database; without one the test is skipped.
func TestSpaceInitializationConcurrencyExternalDatabase(t *testing.T) {
	dialect, dsn := os.Getenv("IAM_LIFECYCLE_DATABASE_TYPE"), os.Getenv("IAM_LIFECYCLE_DATABASE_DSN")
	if dialect == "" && dsn == "" {
		t.Skip("external database is supplied by the PostgreSQL gate")
	}
	require.Contains(t, []string{"postgres", "mysql"}, dialect)
	require.NotEmpty(t, dsn)
	ctx := t.Context()
	// Each run owns only these generated-prefix tables.
	prefix := fmt.Sprintf("fsp_%x_", time.Now().UnixNano())
	router, err := gormx.NewDatasourceRouter(ctx, false, map[string]gormx.Datasource{
		"primary": {Type: dialect, DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.Migrate(ctx, sqlDB, source, dialect))

	runtime := appenv.NewRuntime(appenv.NewIdentity("files-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	ids := &operationServiceIDs{}
	ids.next.Store(10000)
	var eventsMu sync.Mutex
	rootEvents := map[string]int{}
	appender := audit.Appender(func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "file_root_created" {
			eventsMu.Lock()
			rootEvents[fmt.Sprint(event.Detail["scope"])]++
			eventsMu.Unlock()
		}
		return nil
	})
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	objects, err := NewAccountObjects(driver, "local", prefix)
	require.NoError(t, err)
	service := &operationService{
		runtime: runtime, driver: driver, objects: objects, ids: ids, appender: appender,
		budget: operationBudget{100, 1000}, timeout: time.Minute, uploadTimeout: time.Minute, maxUploadBytes: 1 << 20,
		systemPolicy: func(context.Context, *gorm.DB, string, guid.ID) (bool, error) { return true, nil },
	}
	module := &Module{runtime: runtime, ids: ids, appender: appender, references: func(context.Context, *gorm.DB, guid.ID, core.AuthorizationReferences) (bool, error) {
		return true, nil
	}}
	bindings, err := NewResourceBindings(prefix)
	require.NoError(t, err)
	resources, err := NewAccountResourceFiles(objects, bindings, driver, prefix)
	require.NoError(t, err)
	require.NoError(t, resources.Configure(ids, appender))
	require.NoError(t, resources.RegisterPurpose(accountAvatarPurpose(PrincipalAvatarPurpose, false)))
	require.NoError(t, resources.RegisterPurpose(accountAvatarPurpose(PrincipalAvatarCandidatePurpose, true)))

	const workers = 8
	concurrently := func(t *testing.T, initialize func() error) {
		t.Helper()
		start := make(chan struct{})
		failures := make([]error, workers)
		var group sync.WaitGroup
		for i := range workers {
			group.Go(func() {
				<-start
				failures[i] = initialize()
			})
		}
		close(start)
		group.Wait()
		for i, failure := range failures {
			require.NoError(t, failure, "worker %d", i)
		}
	}
	q := query.Bind(db, prefix)
	exactlyOne := func(t *testing.T, owned query.ISpaceDo) {
		t.Helper()
		spaces, err := owned.Find()
		require.NoError(t, err)
		require.Len(t, spaces, 1, "concurrent first use creates one space")
		roots, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(spaces[0].ID), q.Node.ParentID.IsNull()).Count()
		require.NoError(t, err)
		require.EqualValues(t, 1, roots, "and one physical root")
	}
	own := func(principalID guid.ID) context.Context {
		return authn.WithClaims(ctx, &authn.Claims{PrincipalID: principalID})
	}

	t.Run("account browsing", func(t *testing.T) {
		concurrently(t, func() error {
			_, err := service.ensureOwnedRoot(own(70))
			return err
		})
		exactlyOne(t, q.Space.WithContext(ctx).Where(q.Space.PrincipalID.Eq(guid.ID(70))))
	})
	t.Run("account resource", func(t *testing.T) {
		concurrently(t, func() error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				_, _, err := resources.ensureRoot(ctx, tx, 71, 71, time.Now())
				return err
			})
		})
		exactlyOne(t, q.Space.WithContext(ctx).Where(q.Space.PrincipalID.Eq(guid.ID(71))))
	})
	t.Run("tenant", func(t *testing.T) {
		concurrently(t, func() error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				_, err := module.provisionOrganizationRoot(ctx, tx, core.FileRootProvisionInput{Scope: ownerTenant, TenantID: 91, OwnerID: 91}, time.Now())
				return err
			})
		})
		exactlyOne(t, q.Space.WithContext(ctx).Where(q.Space.TenantID.Eq(guid.ID(91))))
	})
	t.Run("system", func(t *testing.T) {
		concurrently(t, func() error {
			_, err := service.ensureSystemRoot(systemFileContext(own(70)))
			return err
		})
		exactlyOne(t, q.Space.WithContext(ctx).Where(q.Space.ScopeKind.Eq(spaceSystem)))
	})
	t.Run("legacy avatars", func(t *testing.T) {
		// Every replica migrates legacy avatars while it initializes.
		now := time.Now().UTC().Truncate(time.Millisecond)
		future := now.Add(time.Hour).UnixMilli()
		token := strings.Repeat("d", 48)
		for _, legacy := range []struct {
			id, principal guid.ID
			purpose, key  string
			expires       *int64
		}{
			{1001, 72, "avatar", "avatars/72/current.png", nil},
			{1002, 72, "avatar_candidate", "avatars/72/" + token + ".png", &future},
			{1003, 73, "avatar", "avatars/73/current.png", nil},
		} {
			require.NoError(t, q.Object.WithContext(ctx).Create(&model.Object{
				ID: 2000 + legacy.id, BackendID: "local", StorageKey: legacy.key, URI: "file:///" + legacy.key,
				State: "verified", Size: 3, MIME: "image/png", Type: "image", Subtype: "png",
				MetadataJSON: "{}", MetadataState: "unknown", Version: 1, CreatedAt: now.UnixMilli(),
			}))
			require.NoError(t, q.AccountReference.WithContext(ctx).Create(&model.AccountReference{
				ID: legacy.id, PrincipalID: legacy.principal, ObjectID: 2000 + legacy.id, Purpose: legacy.purpose,
				State: "active", ExpiresAt: legacy.expires, CreatedAt: now.UnixMilli(),
			}))
		}
		concurrently(t, func() error {
			return resources.MigrateLegacyAvatars(ctx, db, now)
		})
		for _, principal := range []guid.ID{72, 73} {
			exactlyOne(t, q.Space.WithContext(ctx).Where(q.Space.PrincipalID.Eq(principal)))
			current, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ResourceID.Eq(principal), q.ResourceBinding.Purpose.Eq(PrincipalAvatarPurpose), q.ResourceBinding.State.Eq("active")).Count()
			require.NoError(t, err)
			require.EqualValues(t, 1, current, "principal %d keeps one current avatar", principal)
		}
		candidates, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.Purpose.Eq(PrincipalAvatarCandidatePurpose)).Count()
		require.NoError(t, err)
		require.EqualValues(t, 1, candidates)
		active, err := q.AccountReference.WithContext(ctx).Where(q.AccountReference.State.Eq("active")).Count()
		require.NoError(t, err)
		require.Zero(t, active, "every legacy reference is retired once")
	})
	eventsMu.Lock()
	defer eventsMu.Unlock()
	require.Equal(t, 1, rootEvents[ownerSystem], "only the call that created the system space records its root")
	require.Equal(t, 4, rootEvents[ownerAccount], "one account root event per principal")
}
