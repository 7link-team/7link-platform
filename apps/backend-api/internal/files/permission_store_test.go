package files

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func bindRootFixture(t *testing.T, db *gorm.DB, tree directoryTree, id guid.ID, binding rootBinding) {
	t.Helper()
	node := treeNode(t, db, tree.prefix, id)
	spaceVersion := treeSpaceRevision(t, db, tree.prefix, node.SpaceID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.bindRoot(t.Context(), tx, node.SpaceID, nodeRevision{id, node.Version}, spaceVersion, binding, time.Now())
	}))
}

func replaceACLFixture(t *testing.T, db *gorm.DB, tree directoryTree, id guid.ID, stop bool, rules ...nodeACLRule) {
	t.Helper()
	node := treeNode(t, db, tree.prefix, id)
	spaceVersion := treeSpaceRevision(t, db, tree.prefix, node.SpaceID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.replaceNodeACL(t.Context(), tx, node.SpaceID, nodeRevision{id, node.Version}, spaceVersion, stop, 70, rules, time.Now(), operationNext(t))
	}))
}

func loadACLFixture(t *testing.T, db *gorm.DB, tree directoryTree, id guid.ID) storedNodePermissions {
	t.Helper()
	var result storedNodePermissions
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = tree.loadNodePermissions(t.Context(), tx, 1, id)
		return err
	}))
	return result
}

func TestStoredNodePermissionsRootBoundariesAndInheritance(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	actor, department := aclSubject{"principal", 70}, aclSubject{"department", 50}
	subjects := map[aclSubject]struct{}{actor: {}, department: {}}
	before := immediateDatabaseSnapshot(t, db, tree)
	err := db.Transaction(func(tx *gorm.DB) error { _, err := tree.loadNodePermissions(t.Context(), tx, 1, 31); return err })
	require.ErrorIs(t, err, errRootUnbound)
	require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
	bindRootFixture(t, db, tree, 2, rootBinding{scope: "tenant", ownerID: 90})
	replaceACLFixture(t, db, tree, 2, true, aclFixtureRule(actor, "deny", "self_and_descendants", "read"))
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "department", ownerID: 50})
	replaceACLFixture(t, db, tree, 3, true, aclFixtureRule(department, "allow", "self_and_descendants", "read", "download"))
	permissions := loadACLFixture(t, db, tree, 31)
	require.Equal(t, guid.ID(3), permissions.rootID)
	require.Equal(t, rootBinding{scope: "department", ownerID: 50}, permissions.root)
	result, err := evaluateNodeACL("read", subjects, permissions.layers)
	require.NoError(t, err)
	require.True(t, result.allowed, "namespace ACL cannot cross an independent file root")
	replaceACLFixture(t, db, tree, 10, false, aclFixtureRule(actor, "deny", "descendants", "read"))
	replaceACLFixture(t, db, tree, 11, false, aclFixtureRule(actor, "allow", "self_and_descendants", "read"))
	result, err = evaluateNodeACL("read", subjects, loadACLFixture(t, db, tree, 31).layers)
	require.NoError(t, err)
	require.False(t, result.allowed)
	require.Equal(t, []guid.ID{10}, result.deniedNodes)
	replaceACLFixture(t, db, tree, 11, true, aclFixtureRule(actor, "allow", "self_and_descendants", "read"))
	permissions = loadACLFixture(t, db, tree, 31)
	require.Len(t, permissions.layers, 2)
	result, err = evaluateNodeACL("read", subjects, permissions.layers)
	require.NoError(t, err)
	require.True(t, result.allowed)
	result, err = evaluateNodeACL("download", subjects, permissions.layers)
	require.NoError(t, err)
	require.False(t, result.allowed, "interrupted inheritance must not retain old download grants")
	bindRootFixture(t, db, tree, 11, rootBinding{scope: "personal", ownerID: 70})
	permissions = loadACLFixture(t, db, tree, 31)
	require.Equal(t, guid.ID(11), permissions.rootID)
	require.Equal(t, guid.ID(70), permissions.root.ownerID)
	replaceACLFixture(t, db, tree, 11, true)
	result, err = evaluateNodeACL("read", subjects, loadACLFixture(t, db, tree, 31).layers)
	require.NoError(t, err)
	require.False(t, result.allowed, "an empty persisted ACL grants nothing")
}

func TestRootBindingShapesAndAccountIsolation(t *testing.T) {
	for _, binding := range []rootBinding{
		{scope: "tenant", ownerID: 90}, {scope: "entity", ownerID: 80},
		{scope: "department", ownerID: 50}, {scope: "personal", ownerID: 70},
	} {
		t.Run(binding.scope, func(t *testing.T) {
			db, tree, _ := newOperationTree(t)
			bindRootFixture(t, db, tree, 3, binding)
			require.Equal(t, binding, loadACLFixture(t, db, tree, 31).root)
		})
	}
	db, tree, _ := newOperationTree(t)
	require.NoError(t, tree.createSpace(t.Context(), db, 100, 101, 70, "account", time.Now()))
	bindRootFixture(t, db, tree, 101, rootBinding{scope: "account", ownerID: 70})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		permissions, err := tree.loadNodePermissions(t.Context(), tx, 100, 101)
		if err != nil {
			return err
		}
		require.Equal(t, rootBinding{scope: "account", ownerID: 70}, permissions.root)
		require.Nil(t, permissions.space.TenantID)
		require.Equal(t, guid.ID(70), *permissions.space.PrincipalID)
		return nil
	}))
	err := db.Transaction(func(tx *gorm.DB) error { _, err := tree.loadNodePermissions(t.Context(), tx, 1, 101); return err })
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRootBindingRejectsInvalidAndStaleEdits(t *testing.T) {
	for _, failure := range []string{"empty", "account-in-tenant", "tenant-owner-mismatch", "owner-missing", "negative", "file", "stale-node", "stale-space", "rebind", "nil-db", "outside-transaction"} {
		t.Run(failure, func(t *testing.T) {
			db, tree, _ := newOperationTree(t)
			node := nodeRevision{3, treeNode(t, db, tree.prefix, 3).Version}
			spaceVersion := treeSpaceRevision(t, db, tree.prefix, 1)
			binding, want := rootBinding{scope: "entity", ownerID: 80}, errFileACL
			switch failure {
			case "empty":
				binding = rootBinding{}
			case "account-in-tenant":
				binding = rootBinding{scope: "account", ownerID: 70}
			case "tenant-owner-mismatch":
				binding = rootBinding{scope: "tenant", ownerID: 91}
			case "owner-missing":
				binding.ownerID = 0
			case "negative":
				binding.ownerID = -1
			case "file":
				node = nodeRevision{31, treeNode(t, db, tree.prefix, 31).Version}
			case "stale-node":
				node.Version++
				want = errTreeConflict
			case "stale-space":
				spaceVersion++
				want = errTreeConflict
			case "rebind":
				bindRootFixture(t, db, tree, 3, binding)
				node.Version = treeNode(t, db, tree.prefix, 3).Version
				spaceVersion = treeSpaceRevision(t, db, tree.prefix, 1)
			case "outside-transaction":
				want = ErrAccountTransaction
			}
			before := immediateDatabaseSnapshot(t, db, tree)
			run := func(tx *gorm.DB) error {
				return tree.bindRoot(t.Context(), tx, 1, node, spaceVersion, binding, time.Now())
			}
			var err error
			if failure == "nil-db" {
				err = run(nil)
			} else if failure == "outside-transaction" {
				err = run(db)
			} else {
				err = db.Transaction(run)
			}
			require.ErrorIs(t, err, want)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
		})
	}
}

func TestStoredACLReplacementRollsBackAndFencesVersions(t *testing.T) {
	for _, failure := range []string{"stale-node", "stale-space", "invalid-rule", "duplicate-rule", "budget", "id-source", "zero-id", "late-sql", "audit", "root-inherit", "nil-db", "outside-transaction"} {
		t.Run(failure, func(t *testing.T) {
			db, tree, _ := newOperationTree(t)
			bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
			actor := aclSubject{"principal", 70}
			replaceACLFixture(t, db, tree, 3, true, aclFixtureRule(actor, "allow", "self_and_descendants", "read"))
			node := nodeRevision{3, treeNode(t, db, tree.prefix, 3).Version}
			spaceVersion := treeSpaceRevision(t, db, tree.prefix, 1)
			rules := []nodeACLRule{aclFixtureRule(actor, "allow", "self_and_descendants", "read", "download")}
			next, stop, want := operationNext(t), true, errFileACL
			switch failure {
			case "stale-node":
				node.Version++
				want = errTreeConflict
			case "stale-space":
				spaceVersion++
				want = errTreeConflict
			case "invalid-rule":
				rules[0].actions = []string{"*"}
			case "duplicate-rule":
				rules = append(rules, rules[0])
			case "budget":
				rules = nil
				for index := range maxNodeACLRules + 1 {
					rules = append(rules, aclFixtureRule(aclSubject{"principal", guid.ID(index + 1)}, "allow", "self", "read"))
				}
			case "id-source":
				want = errors.New("IDs unavailable")
				next = func() (int64, error) { return 0, want }
			case "zero-id":
				next = func() (int64, error) { return 0, nil }
			case "late-sql":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_acl BEFORE INSERT ON "+tree.prefix+"file_node_acl BEGIN SELECT RAISE(ABORT, 'ACL write refused'); END").Error)
			case "audit":
				want = errors.New("audit unavailable")
			case "root-inherit":
				stop = false
			case "outside-transaction":
				want = ErrAccountTransaction
			}
			before := immediateDatabaseSnapshot(t, db, tree)
			run := func(tx *gorm.DB) error {
				if err := tree.replaceNodeACL(t.Context(), tx, 1, node, spaceVersion, stop, 70, rules, time.Now(), next); err != nil {
					return err
				}
				if failure == "audit" {
					return want
				}
				return nil
			}
			var err error
			if failure == "nil-db" {
				err = run(nil)
			} else if failure == "outside-transaction" {
				err = run(db)
			} else {
				err = db.Transaction(run)
			}
			if failure == "late-sql" {
				require.ErrorContains(t, err, "ACL write refused")
			} else {
				require.ErrorIs(t, err, want)
			}
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree), "direct ACL, node and space revision all roll back")
		})
	}
}

func TestStoredNodePermissionsRejectsHiddenMissingAndOverBudget(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
	require.ErrorIs(t, func() error { _, err := tree.loadNodePermissions(t.Context(), nil, 1, 31); return err }(), errFileACL)
	require.ErrorIs(t, func() error { _, err := tree.loadNodePermissions(t.Context(), db, 1, 31); return err }(), ErrAccountTransaction)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := db.Transaction(func(tx *gorm.DB) error { _, err := tree.loadNodePermissions(ctx, tx, 1, 31); return err })
	require.ErrorIs(t, err, context.Canceled)
	q := query.Bind(db, tree.prefix)
	rows := make([]*model.NodeACL, 0, maxNodeACLRules+1)
	for index := range maxNodeACLRules + 1 {
		rows = append(rows, &model.NodeACL{ID: guid.ID(index + 100), SpaceID: 1, NodeID: 3, SubjectKind: "principal", SubjectID: guid.ID(index + 1), Effect: "allow", Action: "read", Applies: "self_and_descendants", CreatedBy: 70})
	}
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(rows...))
	err = db.Transaction(func(tx *gorm.DB) error { _, err := tree.loadNodePermissions(t.Context(), tx, 1, 31); return err })
	require.ErrorIs(t, err, errFileACL)
	// An inheritance break makes unreachable ancestor rules irrelevant.
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).UpdateSimple(q.Node.ACLStopInheritance.Value(true))
	require.NoError(t, err)
	require.Len(t, loadACLFixture(t, db, tree, 31).layers, 2)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(11))).UpdateSimple(q.Node.State.Value("trashed"), q.Node.LiveSlot.Null())
	require.NoError(t, err)
	err = db.Transaction(func(tx *gorm.DB) error { _, err := tree.loadNodePermissions(t.Context(), tx, 1, 31); return err })
	require.ErrorIs(t, err, errHierarchy, "hidden ancestor cannot be omitted to bypass authorization")
}

func TestStoredNodePermissionsBatchRejectsOverBudgetACL(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
	rows := make([]*model.NodeACL, 0, maxNodeACLRules+1)
	for index := range maxNodeACLRules + 1 {
		rows = append(rows, &model.NodeACL{
			ID: guid.ID(index + 100), SpaceID: 1, NodeID: 3,
			SubjectKind: "principal", SubjectID: guid.ID(index + 1),
			Effect: "allow", Action: "read", Applies: "self_and_descendants", CreatedBy: 70,
		})
	}
	require.NoError(t, query.Bind(db, tree.prefix).NodeACL.WithContext(t.Context()).Create(rows...))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := tree.loadNodePermissionsBatch(t.Context(), tx, 1, []guid.ID{30, 31})
		return err
	}), errFileACL)
}

func TestStoredNodePermissionsDoesNotAcquireMutationLock(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
	var updates atomic.Int64
	const callback = "test:reject-permission-read-update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(*gorm.DB) {
		updates.Add(1)
	}))
	t.Cleanup(func() { db.Callback().Update().Remove(callback) })

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := tree.loadNodePermissions(t.Context(), tx, 1, 31)
		return err
	}))
	assert.Zero(t, updates.Load(), "permission reads must not update the space revision to acquire a lock")
}

func TestStoredACLConcurrentEditsRejectLostUpdate(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
	node := nodeRevision{3, treeNode(t, db, tree.prefix, 3).Version}
	spaceVersion := treeSpaceRevision(t, db, tree.prefix, 1)
	ids := &operationServiceIDs{}
	ids.next.Store(10000)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			actor := guid.ID(index + 70)
			errs[index] = db.Transaction(func(tx *gorm.DB) error {
				return tree.replaceNodeACL(t.Context(), tx, 1, node, spaceVersion, true, actor, []nodeACLRule{aclFixtureRule(aclSubject{"principal", actor}, "allow", "self", "read")}, time.Now(), ids.Next)
			})
		}()
	}
	close(start)
	wg.Wait()
	succeeded, conflicted := 0, 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, errTreeConflict)
			conflicted++
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, conflicted)
	require.Equal(t, node.Version+1, treeNode(t, db, tree.prefix, 3).Version)
	require.Equal(t, spaceVersion+1, treeSpaceRevision(t, db, tree.prefix, 1))
	q := query.Bind(db, tree.prefix)
	rows, err := q.NodeACL.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, rows[0].SubjectID, rows[0].CreatedBy)
}

func TestNodeRootBindingFailsClosedOnCorruptMetadata(t *testing.T) {
	one := 1
	tenantID := guid.ID(90)
	principalID := guid.ID(70)
	scope := "entity"
	ownerID := guid.ID(80)
	systemSpace := &model.Space{ID: 1, ScopeKind: "system", SystemSlot: &one}
	systemRoot := &model.Node{ID: 2, SpaceID: 1, Kind: "directory", State: "active"}

	binding, err := nodeRootBinding(systemSpace, systemRoot)
	require.NoError(t, err)
	require.Equal(t, rootBinding{scope: "system"}, binding)

	for name, fixture := range map[string]struct {
		space *model.Space
		node  *model.Node
	}{
		"nil node": {space: systemSpace},
		"system root with explicit scope": {
			space: systemSpace,
			node:  &model.Node{ID: 2, SpaceID: 1, Kind: "directory", State: "active", RootScope: &scope},
		},
		"one sided root binding": {
			space: &model.Space{ID: 1, ScopeKind: "tenant", TenantID: &tenantID},
			node:  &model.Node{ID: 2, SpaceID: 1, Kind: "directory", State: "active", RootScope: &scope},
		},
		"tenant space with principal": {
			space: &model.Space{ID: 1, ScopeKind: "tenant", TenantID: &tenantID, PrincipalID: &principalID},
			node: &model.Node{
				ID: 2, SpaceID: 1, Kind: "directory", State: "active",
				RootScope: &scope, RootOwnerID: &ownerID,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			binding, err := nodeRootBinding(fixture.space, fixture.node)
			require.ErrorIs(t, err, errFileACL)
			require.Equal(t, rootBinding{}, binding)
		})
	}
}

func TestStoredNodePermissionsBatchValidatesTargetsAndDeduplicates(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})

	for name, call := range map[string]func() error{
		"nil transaction": func() error {
			_, _, err := tree.loadNodePermissionsBatch(t.Context(), nil, 1, []guid.ID{30})
			return err
		},
		"outside transaction": func() error {
			_, _, err := tree.loadNodePermissionsBatch(t.Context(), db, 1, []guid.ID{30})
			return err
		},
		"zero target": func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				_, _, err := tree.loadNodePermissionsBatch(t.Context(), tx, 1, []guid.ID{0})
				return err
			})
		},
		"too many targets": func() error {
			ids := make([]guid.ID, maxBrowserPage+maxNodeDepth+2)
			for index := range ids {
				ids[index] = guid.ID(index + 1)
			}
			return db.Transaction(func(tx *gorm.DB) error {
				_, _, err := tree.loadNodePermissionsBatch(t.Context(), tx, 1, ids)
				return err
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if name == "outside transaction" {
				require.ErrorIs(t, err, ErrAccountTransaction)
			} else {
				require.ErrorIs(t, err, errFileACL)
			}
		})
	}

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		permissions, targetErrors, err := tree.loadNodePermissionsBatch(t.Context(), tx, 1, []guid.ID{30, 30})
		require.NoError(t, err)
		require.Empty(t, targetErrors)
		require.Len(t, permissions, 1)
		require.Contains(t, permissions, guid.ID(30))
		return nil
	}))
}

func TestStoredNodePermissionsPropagateEveryReachedStatementFailure(t *testing.T) {
	type faultCase struct {
		name, table string
		skip        int64
		batch       bool
	}
	cases := []faultCase{
		{name: "single space", table: "file_spaces"},
		{name: "single path", table: "file_nodes"},
		{name: "single ACL", table: "file_node_acl"},
		{name: "batch space", table: "file_spaces", batch: true},
		{name: "batch ACL", table: "file_node_acl", batch: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, tree, _ := newOperationTree(t)
			bindRootFixture(t, db, tree, 3, rootBinding{scope: "entity", ownerID: 80})
			fault := failTableAfter(t, db, testCase.table, testCase.skip)
			err := db.Transaction(func(tx *gorm.DB) error {
				if testCase.batch {
					_, _, err := tree.loadNodePermissionsBatch(t.Context(), tx, 1, []guid.ID{30, 31})
					return err
				}
				_, err := tree.loadNodePermissions(t.Context(), tx, 1, 30)
				return err
			})
			if !fault.Fired() {
				t.Skip("target statement was not reached")
			}
			require.ErrorIs(t, err, errInjectedTableFault)
		})
	}
}
