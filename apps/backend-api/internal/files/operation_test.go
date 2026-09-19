package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func operationAllow(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }

var operationTestWorker atomic.Uint32

func operationNext(t *testing.T) func() (int64, error) {
	t.Helper()
	// Independent generators cannot share a worker within one Sonyflake tick.
	generator, err := guid.New(uint16(operationTestWorker.Add(1)))
	require.NoError(t, err)
	return generator.Next
}

func operationFixtureFile(t *testing.T, db *gorm.DB, tree directoryTree, id, parentID guid.ID, name string) {
	t.Helper()
	q := query.Bind(db, tree.prefix)
	objectID, versionID := id+1000, id+2000
	object := model.Object{ID: objectID, BackendID: "local", StorageKey: fmt.Sprintf("objects/%d", objectID), URI: fmt.Sprintf("file:///backend/objects/%d", objectID), State: "verified", Size: 3, MIME: "application/octet-stream", Type: "application", Subtype: "octet-stream", MetadataJSON: "{}", MetadataState: "unknown", Version: 1}
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&object))
	path, err := tree.pathNodes(t.Context(), db, 1, parentID)
	require.NoError(t, err)
	slot := 1
	_, key, err := PortableName(name)
	require.NoError(t, err)
	file := model.Node{ID: id, SpaceID: 1, ParentID: &parentID, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: name, NameKey: key, Ext: Extension(name), State: "active", Version: 1}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, tree.prefix)
		if err := q.Node.WithContext(t.Context()).Create(&file); err != nil {
			return err
		}
		links := []*model.NodeClosure{{SpaceID: 1, AncestorID: id, DescendantID: id, Depth: 0}}
		for index, ancestor := range path {
			links = append(links, &model.NodeClosure{SpaceID: 1, AncestorID: ancestor.ID, DescendantID: id, Depth: len(path) - index})
		}
		if err := q.NodeClosure.WithContext(t.Context()).Create(links...); err != nil {
			return err
		}
		return tree.appendVersion(t.Context(), tx, model.Version{ID: versionID, SpaceID: 1, NodeID: id, Number: 1, ObjectID: objectID, Ext: file.Ext, CreatedBy: 90}, 1, time.Now())
	}))
}

func newOperationTree(t *testing.T) (*gorm.DB, directoryTree, operationRequest) {
	t.Helper()
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", time.Now()))
	for _, entry := range []struct {
		id, parent guid.ID
		name       string
	}{{3, 2, "Source"}, {6, 2, "Destination"}, {10, 3, "Shared"}, {11, 10, "Nested"}, {20, 6, "Shared"}, {21, 20, "Nested"}} {
		_, err := tree.createDirectory(t.Context(), db, 1, entry.id, entry.parent, entry.name, time.Now())
		require.NoError(t, err)
	}
	operationFixtureFile(t, db, tree, 30, 11, "new.txt")
	operationFixtureFile(t, db, tree, 31, 11, "clash.txt")
	operationFixtureFile(t, db, tree, 32, 10, "root.txt")
	operationFixtureFile(t, db, tree, 40, 21, "CLASH.txt")
	operationFixtureFile(t, db, tree, 41, 21, "keep.txt")
	request := operationRequest{SpaceID: 1, Action: "copy", DestinationID: 6, Sources: []guid.ID{10}, FilePolicy: "overwrite", DirectoryPolicy: "merge", TypePolicy: "replace"}
	return db, tree, request
}

func prepareFixtureOperation(t *testing.T, db *gorm.DB, tree directoryTree, id guid.ID, key string, request operationRequest, budget operationBudget) *model.Operation {
	t.Helper()
	now := time.Now()
	op, err := tree.prepareOperation(t.Context(), db, id, 90, key, request, budget, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.NoError(t, err)
	require.Equal(t, "ready", op.State)
	return op
}

func executeFixtureOperation(t *testing.T, db *gorm.DB, tree directoryTree, op *model.Operation, access operationAccess) (operationResult, error) {
	t.Helper()
	var result operationResult
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = tree.executeOperation(t.Context(), tx, op.SpaceID, op.ID, op.ActorID, op.Revision, time.Now(), operationNext(t), access)
		return err
	})
	return result, err
}

func operationRecord(t *testing.T, db *gorm.DB, tree directoryTree, id guid.ID) *model.Operation {
	t.Helper()
	q := query.Bind(db, tree.prefix)
	op, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(id)).Take()
	require.NoError(t, err)
	return op
}

func TestOperationRecursiveCopyMergeOverwriteAndSucceededReplay(t *testing.T) {
	db, tree, request := newOperationTree(t)
	op := prepareFixtureOperation(t, db, tree, 100, "copy-merge", request, operationBudget{100, 1000})
	require.Equal(t, 5, op.ItemCount)
	q := query.Bind(db, tree.prefix)
	result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	require.Equal(t, 1, result.Overwritten)
	require.Equal(t, []guid.ID{20}, result.Roots)
	require.Equal(t, "active", treeNode(t, db, tree.prefix, 10).State)
	for _, path := range []string{"/Destination/Shared/Nested/new.txt", "/Destination/Shared/Nested/CLASH.txt", "/Destination/Shared/Nested/keep.txt", "/Destination/Shared/root.txt", "/Source/Shared/Nested/new.txt"} {
		_, err := tree.resolvePath(t.Context(), db, 1, path)
		require.NoError(t, err, path)
	}
	current := treeNode(t, db, tree.prefix, 40)
	versions, err := q.Version.WithContext(t.Context()).Where(q.Version.NodeID.Eq(guid.ID(40))).Order(q.Version.Number.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, guid.ID(1040), versions[0].ObjectID)
	require.Equal(t, guid.ID(1031), versions[1].ObjectID)
	require.Equal(t, guid.ID(2031), *versions[1].SourceVersionID)
	require.Equal(t, versions[1].ID, *current.CurrentVersionID)
	objects, err := q.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 5, objects, "virtual copies do not duplicate physical content")
	pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
	require.NoError(t, err)
	require.Zero(t, pins)
	stored := operationRecord(t, db, tree, op.ID)
	require.Equal(t, "succeeded", stored.State)
	require.NotNil(t, stored.CompletedAt)
	beforeRevision := treeSpaceRevision(t, db, tree.prefix, 1)
	beforeVersionCount, err := q.Version.WithContext(t.Context()).Count()
	require.NoError(t, err)
	replay, err := executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	require.Equal(t, result, replay)
	require.Equal(t, beforeRevision, treeSpaceRevision(t, db, tree.prefix, 1))
	afterVersionCount, err := q.Version.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Equal(t, beforeVersionCount, afterVersionCount)
	denied := errors.New("result root access revoked")
	_, err = executeFixtureOperation(t, db, tree, op, func(_ context.Context, _ *gorm.DB, _, _, nodeID guid.ID, action string) error {
		if nodeID == 20 && action == "read" {
			return denied
		}
		return nil
	})
	require.ErrorIs(t, err, denied, "succeeded replay still checks current result-root access")
	require.Equal(t, beforeRevision, treeSpaceRevision(t, db, tree.prefix, 1))
	retried, err := tree.prepareOperation(t.Context(), db, 101, 90, "copy-merge", request, operationBudget{100, 1000}, time.Now().Add(time.Hour), time.Now(), operationNext(t), operationAllow)
	require.NoError(t, err)
	require.Equal(t, op.ID, retried.ID)
	request.FilePolicy = "skip"
	_, err = tree.prepareOperation(t.Context(), db, 102, 90, "copy-merge", request, operationBudget{100, 1000}, time.Now().Add(time.Hour), time.Now(), operationNext(t), operationAllow)
	require.ErrorIs(t, err, errOperationKey)
}

func TestOperationMoveMergeKeepsSkippedSources(t *testing.T) {
	for _, policy := range []string{"skip", "overwrite"} {
		t.Run(policy, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.Action, request.FilePolicy = "move", policy
			op := prepareFixtureOperation(t, db, tree, 100, "move-merge", request, operationBudget{100, 1000})
			result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
			require.NoError(t, err)
			require.Equal(t, 2, result.Moved)
			if policy == "skip" {
				require.Equal(t, 1, result.Skipped)
				require.Equal(t, "active", treeNode(t, db, tree.prefix, 10).State)
				require.Equal(t, "active", treeNode(t, db, tree.prefix, 11).State)
				require.Equal(t, "active", treeNode(t, db, tree.prefix, 31).State)
				_, err := tree.resolvePath(t.Context(), db, 1, "/Source/Shared/Nested/clash.txt")
				require.NoError(t, err)
			} else {
				require.Equal(t, 1, result.Overwritten)
				for _, id := range []guid.ID{10, 11, 31} {
					require.Equal(t, "trashed", treeNode(t, db, tree.prefix, id).State)
				}
			}
			for _, id := range []guid.ID{30, 32, 40, 41} {
				require.Equal(t, "active", treeNode(t, db, tree.prefix, id).State)
				_, err := tree.pathNodes(t.Context(), db, 1, id)
				require.NoError(t, err)
			}
		})
	}
}

func TestOperationDirectoryReplaceAndTypeReplaceRecycleTargets(t *testing.T) {
	for _, typeReplacement := range []bool{false, true} {
		t.Run(fmt.Sprint(typeReplacement), func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.Action, request.DirectoryPolicy = "move", "replace"
			if typeReplacement {
				// A source file replaces a same-name directory in the destination.
				request.Sources = []guid.ID{32}
				request.DestinationID = 20
				_, err := tree.createDirectory(t.Context(), db, 1, 22, 20, "root.txt", time.Now())
				require.NoError(t, err)
			}
			op := prepareFixtureOperation(t, db, tree, 100, "replace", request, operationBudget{100, 1000})
			result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
			require.NoError(t, err)
			if typeReplacement {
				require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 22).State)
				require.Equal(t, guid.ID(20), *treeNode(t, db, tree.prefix, 32).ParentID)
				require.Equal(t, []guid.ID{32}, result.Roots)
			} else {
				for _, id := range []guid.ID{20, 21, 40, 41} {
					require.Equal(t, "trashed", treeNode(t, db, tree.prefix, id).State)
				}
				require.Equal(t, guid.ID(6), *treeNode(t, db, tree.prefix, 10).ParentID)
				require.Equal(t, []guid.ID{10}, result.Roots)
			}
		})
	}
}

func TestOperationLatePermissionOrResultFailureRollsBackEverything(t *testing.T) {
	for _, failure := range []string{"permission", "result", "budget"} {
		t.Run(failure, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			budget := operationBudget{100, 1000}
			if failure == "result" {
				request.Action, request.DirectoryPolicy = "move", "replace"
			}
			op := prepareFixtureOperation(t, db, tree, 100, "rollback", request, budget)
			q := query.Bind(db, tree.prefix)
			if failure == "budget" {
				// Preparation now refuses insufficient budgets. Independently test
				// the execution guard by tightening the stored budget after planning:
				// the second clone fails after a clone and overwrite, not before work.
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Update(q.Operation.ClosureBudget, 5)
				require.NoError(t, err)
			}
			revision := treeSpaceRevision(t, db, tree.prefix, 1)
			nodesBefore, err := q.Node.WithContext(t.Context()).Order(q.Node.ID.Asc()).Find()
			require.NoError(t, err)
			versionsBefore, err := q.Version.WithContext(t.Context()).Order(q.Version.ID.Asc()).Find()
			require.NoError(t, err)
			closureBefore, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
			require.NoError(t, err)
			denied := errors.New("revoked target update")
			access := operationAccess(operationAllow)
			if failure == "permission" {
				access = func(_ context.Context, tx *gorm.DB, _, _, nodeID guid.ID, action string) error {
					if nodeID == 40 && action == "update" {
						count, err := query.Bind(tx, tree.prefix).Node.WithContext(t.Context()).Where(q.Node.ID.Gt(guid.ID(10000))).Count()
						require.NoError(t, err)
						require.Positive(t, count, "earlier clone must already exist inside the uncommitted transaction")
						return denied
					}
					return nil
				}
			}
			if failure == "result" {
				require.NoError(t, db.Exec("CREATE TRIGGER fail_operation_result BEFORE UPDATE ON "+query.BindNames(tree.prefix).Operations.String()+" WHEN NEW.state = 'succeeded' BEGIN SELECT RAISE(ABORT, 'late result failure'); END").Error)
			}
			_, err = executeFixtureOperation(t, db, tree, op, access)
			require.Error(t, err)
			if failure == "permission" {
				require.ErrorIs(t, err, denied)
			}
			require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
			nodesAfter, err := q.Node.WithContext(t.Context()).Order(q.Node.ID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, nodesBefore, nodesAfter)
			versionsAfter, err := q.Version.WithContext(t.Context()).Order(q.Version.ID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, versionsBefore, versionsAfter)
			closureAfter, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, closureBefore, closureAfter)
			trashCount, err := q.TrashBatch.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, trashCount, "late result failure also rolls back target recycling")
			trashRoots, err := q.TrashRoot.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, trashRoots)
			require.Equal(t, "ready", operationRecord(t, db, tree, op.ID).State)
			pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.OperationID.Eq(op.ID), q.OperationItem.PinnedNodeID.IsNotNull()).Count()
			require.NoError(t, err)
			require.EqualValues(t, 5, pins)
		})
	}
}

func TestOperationPlansPagedNormalizeReleaseAndExpiry(t *testing.T) {
	db, tree := newTrashTree(t)
	for i := 0; i < 105; i++ {
		_, err := tree.createDirectory(t.Context(), db, 1, guid.ID(1000+i), 3, fmt.Sprintf("Child-%03d", i), time.Now())
		require.NoError(t, err)
	}
	request := operationRequest{SpaceID: 1, Action: "copy", DestinationID: 6, Sources: []guid.ID{5, 3, 3}, FilePolicy: "overwrite", DirectoryPolicy: "merge", TypePolicy: "replace"}
	op := prepareFixtureOperation(t, db, tree, 100, "paged", request, operationBudget{200, 10000})
	require.Equal(t, 108, op.ItemCount)
	var roots []guid.ID
	require.NoError(t, json.Unmarshal([]byte(op.RootsJSON), &roots))
	require.Equal(t, []guid.ID{3}, roots)
	q := query.Bind(db, tree.prefix)
	items, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.OperationID.Eq(op.ID)).Order(q.OperationItem.Ordinal.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, items, 108)
	for i, item := range items {
		require.Equal(t, i+1, item.Ordinal)
	}
	_, err = executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	second := prepareFixtureOperation(t, db, tree, 101, "expire", request, operationBudget{200, 10000})
	after, err := tree.expireOperations(t.Context(), db, time.Now(), 0, 200)
	require.NoError(t, err)
	require.Zero(t, after)
	after, err = tree.expireOperations(t.Context(), db, time.UnixMilli(second.ExpiresAt).Add(time.Second), 0, 200)
	require.NoError(t, err)
	require.Zero(t, after, "exhausted sweep resets cursor so an older plan that expires later is not starved")
	expired := operationRecord(t, db, tree, second.ID)
	require.Equal(t, "failed", expired.State)
	require.Equal(t, "plan_expired", expired.ErrorCode)
	pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
	require.NoError(t, err)
	require.Zero(t, pins)
	_, err = executeFixtureOperation(t, db, tree, second, operationAllow)
	require.ErrorIs(t, err, errOperationState)
	require.Equal(t, "succeeded", operationRecord(t, db, tree, op.ID).State, "expiry cannot undo committed success")
}

func TestOperationPinsScopeCancelAndPopulatedMigration(t *testing.T) {
	db, tree, request := newOperationTree(t)
	op := prepareFixtureOperation(t, db, tree, 100, "pin-scope", request, operationBudget{100, 1000})
	q := query.Bind(db, tree.prefix)
	item, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.NodeID.Eq(guid.ID(31))).Take()
	require.NoError(t, err)
	_, err = q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.ID.Eq(item.ID)).UpdateSimple(q.OperationItem.PinnedObjectID.Value(guid.ID(1040)))
	require.Error(t, err, "pinned object must be the pinned version's exact content")
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(31))).UpdateSimple(q.Node.CurrentVersionID.Null())
	require.NoError(t, err)
	_, err = q.Version.WithContext(t.Context()).Where(q.Version.ID.Eq(guid.ID(2031))).Delete()
	require.Error(t, err, "live plan prevents version removal even without a current head")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.finishOperation(t.Context(), tx, op.ID, 90, operationCancelled, "user_cancelled", time.Now()) //nolint:misspell // Preserve the existing cancellation error code.
	}))
	_, err = q.Version.WithContext(t.Context()).Where(q.Version.ID.Eq(guid.ID(2031))).Delete()
	require.NoError(t, err, "cancellation releases live references")
	_, err = executeFixtureOperation(t, db, tree, op, operationAllow)
	require.ErrorIs(t, err, errOperationState)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = tree.prefix
	require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 6))
	require.False(t, db.Migrator().HasTable(query.BindNames(tree.prefix).Operations.String()))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
}

func TestOperationAutoRenameKeepsPhysicalContentAndPortableNames(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.Sources = []guid.ID{31}
	request.DestinationID = 21
	request.FilePolicy = "rename"
	operationFixtureFile(t, db, tree, 42, 21, "clash (1).txt")
	op := prepareFixtureOperation(t, db, tree, 100, "rename", request, operationBudget{100, 1000})
	result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	cloned, err := tree.resolvePath(t.Context(), db, 1, "/Destination/Shared/Nested/clash (2).txt")
	require.NoError(t, err)
	require.NotEqual(t, guid.ID(31), cloned.ID)
	require.Equal(t, "txt", cloned.Ext)
	q := query.Bind(db, tree.prefix)
	version, err := q.Version.WithContext(t.Context()).Where(q.Version.ID.Eq(*cloned.CurrentVersionID)).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(1031), version.ObjectID)
	// Explicit rename also supports duplicating a file into its own parent.
	request.DestinationID = 11
	second := prepareFixtureOperation(t, db, tree, 101, "same-parent-copy", request, operationBudget{100, 1000})
	_, err = executeFixtureOperation(t, db, tree, second, operationAllow)
	require.NoError(t, err)
	_, err = tree.resolvePath(t.Context(), db, 1, "/Source/Shared/Nested/clash (1).txt")
	require.NoError(t, err)
}

func TestOperationPreparationFailureReleasesEarlierCommittedPage(t *testing.T) {
	for _, failure := range []string{"generator", "revision", "budget"} {
		t.Run(failure, func(t *testing.T) {
			db, tree := newTrashTree(t)
			for i := 0; i < 105; i++ {
				_, err := tree.createDirectory(t.Context(), db, 1, guid.ID(1000+i), 3, fmt.Sprintf("Child-%03d", i), time.Now())
				require.NoError(t, err)
			}
			request := operationRequest{SpaceID: 1, Action: "copy", DestinationID: 6, Sources: []guid.ID{3}, FilePolicy: "overwrite", DirectoryPolicy: "merge", TypePolicy: "replace"}
			budget := operationBudget{200, 10000}
			if failure == "budget" {
				budget.Nodes = 101
			}
			next := operationNext(t)
			calls := 0
			generatorFailure := errors.New("lease lost")
			failingNext := func() (int64, error) {
				calls++
				if failure == "generator" && calls == 101 {
					return 0, generatorFailure
				}
				return next()
			}
			accessCalls := 0
			access := func(ctx context.Context, tx *gorm.DB, space, _ guid.ID, _ guid.ID, action string) error {
				if action == "copy" {
					accessCalls++
					if failure == "revision" && accessCalls == 100 {
						// Inject a revision change after the page's initial fence;
						// the next page must not publish a mixed plan as ready.
						q := query.Bind(tx, tree.prefix)
						_, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(space)).UpdateSimple(q.Space.Version.Add(1))
						return err
					}
				}
				return nil
			}
			now := time.Now()
			_, err := tree.prepareOperation(t.Context(), db, 100, 90, "failed-page", request, budget, now.Add(time.Hour), now, failingNext, access)
			require.Error(t, err)
			if failure == "generator" {
				require.ErrorIs(t, err, generatorFailure)
			}
			require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
			q := query.Bind(db, tree.prefix)
			pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
			require.NoError(t, err)
			require.Zero(t, pins)
			items, err := q.OperationItem.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.EqualValues(t, 100, items, "the earlier page was committed, then its live pins were released")
			children, err := tree.children(t.Context(), db, 1, 6, "", 50, 0)
			require.NoError(t, err)
			require.Empty(t, children, "preparation never modifies the virtual tree")
		})
	}
}

func TestOperationStalePlanScopeAndRequiredAuthorization(t *testing.T) {
	db, tree, request := newOperationTree(t)
	now := time.Now()
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "no-access", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), nil)
	require.ErrorIs(t, err, errOperationInput)
	request.FilePolicy = "ask"
	_, err = tree.prepareOperation(t.Context(), db, 100, 90, "undecided", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, errOperationInput, "undecided conflicts must never silently execute")
	request.FilePolicy = "overwrite"
	op := prepareFixtureOperation(t, db, tree, 100, "stale", request, operationBudget{100, 1000})
	_, err = tree.createDirectory(t.Context(), db, 1, 50, 6, "Concurrent", time.Now())
	require.NoError(t, err)
	_, err = executeFixtureOperation(t, db, tree, op, operationAllow)
	require.ErrorIs(t, err, errTreeConflict)
	require.Equal(t, "ready", operationRecord(t, db, tree, 100).State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := tree.executeOperation(t.Context(), tx, 1, op.ID, 91, op.Revision, now, operationNext(t), operationAllow)
		return err
	}), gorm.ErrRecordNotFound)
	denied := errors.New("no destination create")
	_, err = tree.prepareOperation(t.Context(), db, 101, 90, "denied", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return denied })
	require.ErrorIs(t, err, denied)
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 51, 91, "tenant", now))
	request.DestinationID = 51
	_, err = tree.prepareOperation(t.Context(), db, 101, 90, "foreign-destination", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.Error(t, err)
	request.SpaceID = 10
	_, err = tree.prepareOperation(t.Context(), db, 101, 90, "foreign-source", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.Error(t, err)
}

func TestOperationIndexedPlanPagesAndExpiry(t *testing.T) {
	db, tree, request := newOperationTree(t)
	op := prepareFixtureOperation(t, db, tree, 100, "indexes", request, operationBudget{100, 1000})
	names := query.BindNames(tree.prefix)
	plan := explainPlan(t, db, "SELECT id FROM "+names.OperationItems.String()+" WHERE operation_id = ? AND ordinal > ? ORDER BY ordinal LIMIT 100", op.ID, 0)
	require.Contains(t, plan, "SEARCH ")
	require.NotContains(t, plan, "SCAN ")
	require.NotContains(t, plan, "USE TEMP B-TREE")
	plan = explainPlan(t, db, "SELECT id FROM "+names.OperationItems.String()+" WHERE pinned_object_id = ?", 1031)
	require.Contains(t, plan, "idx_file_operation_items_object")
	require.Contains(t, plan, "SEARCH ")
	plan = explainPlan(t, db, "SELECT id FROM "+names.Operations.String()+" WHERE state = ? AND expires_at <= ? LIMIT 100", "ready", op.ExpiresAt)
	require.Contains(t, plan, "idx_file_operations_expiry")
	require.Contains(t, plan, "SEARCH ")
	plan = explainPlan(t, db, "SELECT n.id FROM "+names.NodeClosure.String()+" c JOIN "+names.Nodes.String()+" n ON n.space_id = c.space_id AND n.id = c.descendant_id WHERE c.space_id = ? AND c.ancestor_id = ? AND c.descendant_id > ? AND n.state = ? ORDER BY c.descendant_id LIMIT 100", 1, 10, 0, "active")
	require.Contains(t, plan, "SEARCH ")
	require.NotContains(t, plan, "SCAN ")
	require.NotContains(t, plan, "USE TEMP B-TREE")
	plan = explainPlan(t, db, "SELECT id FROM "+names.Nodes.String()+" WHERE space_id = ? AND parent_id = ? AND state = ? AND id > ? ORDER BY id LIMIT 100", 1, 11, "active", 0)
	require.Contains(t, plan, "idx_file_nodes_children_cursor")
	require.Contains(t, plan, "SEARCH ")
	require.NotContains(t, plan, "USE TEMP B-TREE")
}

func TestOperationCaseSensitiveKeysAndOlderLeaseExpiry(t *testing.T) {
	db, tree, request := newOperationTree(t)
	now := time.Now()
	older, err := tree.prepareOperation(t.Context(), db, 100, 90, "Case-Key", request, operationBudget{100, 1000}, now.Add(2*time.Hour), now, operationNext(t), operationAllow)
	require.NoError(t, err)
	newer, err := tree.prepareOperation(t.Context(), db, 101, 90, "case-key", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.NoError(t, err)
	require.NotEqual(t, older.ID, newer.ID, "opaque idempotency keys are byte-sensitive")
	after, err := tree.expireOperations(t.Context(), db, now.Add(70*time.Minute), 0, 200)
	require.NoError(t, err)
	require.Zero(t, after)
	require.Equal(t, "failed", operationRecord(t, db, tree, newer.ID).State)
	require.Equal(t, "ready", operationRecord(t, db, tree, older.ID).State)
	_, err = tree.expireOperations(t.Context(), db, now.Add(130*time.Minute), after, 200)
	require.NoError(t, err)
	require.Equal(t, "failed", operationRecord(t, db, tree, older.ID).State)
}
