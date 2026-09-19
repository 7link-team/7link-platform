package files

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func executeImmediateFixture(t *testing.T, db *gorm.DB, tree directoryTree, input immediateOperationRequest, access operationAccess) (operationResult, error) {
	t.Helper()
	var result operationResult
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		now := time.Now()
		result, err = tree.executeImmediateOperation(t.Context(), tx, 100, 90, "immediate", input, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), access)
		return err
	})
	return result, err
}

func immediateDatabaseSnapshot(t *testing.T, db *gorm.DB, tree directoryTree) string {
	t.Helper()
	q := query.Bind(db, tree.prefix)
	nodes, err := q.Node.WithContext(t.Context()).Order(q.Node.ID).Find()
	require.NoError(t, err)
	versions, err := q.Version.WithContext(t.Context()).Order(q.Version.ID).Find()
	require.NoError(t, err)
	objects, err := q.Object.WithContext(t.Context()).Order(q.Object.ID).Find()
	require.NoError(t, err)
	closures, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.SpaceID, q.NodeClosure.AncestorID, q.NodeClosure.DescendantID).Find()
	require.NoError(t, err)
	operations, err := q.Operation.WithContext(t.Context()).Order(q.Operation.ID).Find()
	require.NoError(t, err)
	items, err := q.OperationItem.WithContext(t.Context()).Order(q.OperationItem.ID).Find()
	require.NoError(t, err)
	trash, err := q.TrashBatch.WithContext(t.Context()).Order(q.TrashBatch.ID).Find()
	require.NoError(t, err)
	acl, err := q.NodeACL.WithContext(t.Context()).Order(q.NodeACL.ID).Find()
	require.NoError(t, err)
	encoded, err := json.Marshal([]any{treeSpaceRevision(t, db, tree.prefix, 1), nodes, versions, objects, closures, operations, items, trash, acl})
	require.NoError(t, err)
	return string(encoded)
}

func immediateFrontier(t *testing.T, err error) *immediateConflicts {
	t.Helper()
	var frontier *immediateConflicts
	require.ErrorAs(t, err, &frontier)
	require.ErrorIs(t, err, errImmediateConflict)
	require.NotEmpty(t, frontier.Conflicts)
	return frontier
}

func confirmImmediate(input *immediateOperationRequest, frontier *immediateConflicts, policy string) {
	input.ObservedRevision = frontier.SpaceRevision
	for _, conflict := range frontier.Conflicts {
		input.Decisions = append(input.Decisions, immediateDecision{ConflictKey: conflict.Key, Policy: policy})
	}
}

func TestImmediateNestedFrontiersAreTransientAndAtomic(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.FilePolicy, request.DirectoryPolicy, request.TypePolicy = "ask", "ask", "ask"
	input := immediateOperationRequest{operationRequest: request}
	before := immediateDatabaseSnapshot(t, db, tree)
	for _, kind := range []string{"directory", "directory", "file"} {
		_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
		frontier := immediateFrontier(t, err)
		require.Len(t, frontier.Conflicts, 1)
		require.Equal(t, kind, frontier.Conflicts[0].Kind)
		require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree), "even prospective writes must not persist")
		policy := "merge"
		if kind == "file" {
			policy = "overwrite"
			require.Equal(t, guid.ID(2031), frontier.Conflicts[0].Source.ContentVersionID)
			require.EqualValues(t, 3, *frontier.Conflicts[0].Target.Size)
		}
		confirmImmediate(&input, frontier, policy)
	}
	result, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	require.Equal(t, 1, result.Overwritten)
	after := immediateDatabaseSnapshot(t, db, tree)
	replay, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	require.NoError(t, err)
	require.Equal(t, result, replay)
	require.Equal(t, after, immediateDatabaseSnapshot(t, db, tree))
	q := query.Bind(db, tree.prefix)
	count, err := q.OperationItem.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count, "no preparation rows or source pins")
	require.Equal(t, "succeeded", operationRecord(t, db, tree, 100).State)
	input.Decisions[2].Policy = "skip"
	_, err = executeImmediateFixture(t, db, tree, input, operationAllow)
	require.ErrorIs(t, err, errOperationKey)
}

func TestImmediateProspectiveFileCollisionsHaveStableKeys(t *testing.T) {
	for _, action := range []string{"copy", "move"} {
		t.Run(action, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			operationFixtureFile(t, db, tree, 50, 3, "batch.txt")
			operationFixtureFile(t, db, tree, 51, 10, "batch.txt")
			operationFixtureFile(t, db, tree, 52, 11, "batch.txt")
			request.Action, request.Sources, request.FilePolicy = action, []guid.ID{50, 51, 52}, "ask"
			input := immediateOperationRequest{operationRequest: request}
			before := immediateDatabaseSnapshot(t, db, tree)
			_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
			frontier := immediateFrontier(t, err)
			require.Len(t, frontier.Conflicts, 2)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
			if action == "copy" {
				require.Equal(t, guid.ID(50), frontier.Conflicts[0].Target.CreatedFromID)
				require.Zero(t, frontier.Conflicts[0].Target.NodeID)
			} else {
				require.Equal(t, guid.ID(50), frontier.Conflicts[0].Target.NodeID)
			}
			// Overwriting the first conflict changes the next conflict's content
			// and revision. Old choices for that next conflict must be rejected.
			input.ObservedRevision = frontier.SpaceRevision
			input.Decisions = []immediateDecision{{ConflictKey: frontier.Conflicts[0].Key, Policy: "overwrite"}}
			_, err = executeImmediateFixture(t, db, tree, input, operationAllow)
			updated := immediateFrontier(t, err)
			require.Len(t, updated.Conflicts, 1)
			require.NotEqual(t, frontier.Conflicts[1].Key, updated.Conflicts[0].Key)
			require.Equal(t, guid.ID(2051), updated.Conflicts[0].Target.ContentVersionID)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
			confirmImmediate(&input, updated, "overwrite")
			result, err := executeImmediateFixture(t, db, tree, input, operationAllow)
			require.NoError(t, err, "simulation keys must match persisted identities")
			require.Equal(t, 2, result.Overwritten)
			if action == "move" {
				require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 51).State)
				require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 52).State)
			}
		})
	}
}

func TestImmediateSelfCopyOnlyAllowsRename(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.Sources, request.DestinationID, request.FilePolicy = []guid.ID{31}, 11, "ask"
	input := immediateOperationRequest{operationRequest: request}
	before := immediateDatabaseSnapshot(t, db, tree)
	_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	frontier := immediateFrontier(t, err)
	require.Equal(t, []string{"rename"}, frontier.Conflicts[0].Policies)
	confirmImmediate(&input, frontier, "overwrite")
	_, err = executeImmediateFixture(t, db, tree, input, operationAllow)
	require.ErrorIs(t, err, errOperationInput)
	require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
	input.Decisions[0].Policy = "rename"
	result, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	require.NoError(t, err)
	require.Equal(t, 1, result.Created)
	_, err = tree.resolvePath(t.Context(), db, 1, "/Source/Shared/Nested/clash (1).txt")
	require.NoError(t, err)
}

func TestImmediateProspectiveDirectoriesUsePerItemDecisions(t *testing.T) {
	for _, action := range []string{"copy", "move"} {
		t.Run(action, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			for index, parent := range []guid.ID{3, 10, 11} {
				id := guid.ID(60 + index)
				_, err := tree.createDirectory(t.Context(), db, 1, id, parent, "Pack", time.Now())
				require.NoError(t, err)
				operationFixtureFile(t, db, tree, guid.ID(70+index), id, "item.txt")
			}
			request.Action, request.Sources = action, []guid.ID{60, 61, 62}
			request.FilePolicy, request.DirectoryPolicy = "ask", "ask"
			input := immediateOperationRequest{operationRequest: request}
			before := immediateDatabaseSnapshot(t, db, tree)
			_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
			frontier := immediateFrontier(t, err)
			require.Len(t, frontier.Conflicts, 2)
			input.ObservedRevision = frontier.SpaceRevision
			input.Decisions = []immediateDecision{{frontier.Conflicts[0].Key, "merge"}, {frontier.Conflicts[1].Key, "rename"}}
			_, err = executeImmediateFixture(t, db, tree, input, operationAllow)
			nested := immediateFrontier(t, err)
			require.Len(t, nested.Conflicts, 1)
			require.Equal(t, "file", nested.Conflicts[0].Kind)
			if action == "copy" {
				require.Equal(t, guid.ID(60), nested.Conflicts[0].Parent.CreatedFromID)
			}
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
			confirmImmediate(&input, nested, "overwrite")
			result, err := executeImmediateFixture(t, db, tree, input, operationAllow)
			require.NoError(t, err)
			require.Equal(t, 1, result.Overwritten)
			for _, path := range []string{"/Destination/Pack/item.txt", "/Destination/Pack (1)/item.txt"} {
				_, err := tree.resolvePath(t.Context(), db, 1, path)
				require.NoError(t, err)
			}
		})
	}
}

func TestImmediateConcurrentReplayPublishesOnce(t *testing.T) {
	const workers = 4
	db, tree, request := newOperationTree(t)
	// Exercise the SQLite write fence across independent pooled connections.
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(workers)
	input := immediateOperationRequest{operationRequest: request}
	results := make([]operationResult, workers)
	errors := make([]error, workers)
	next := operationNext(t)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			errors[index] = db.Transaction(func(tx *gorm.DB) error {
				now := time.Now()
				var err error
				results[index], err = tree.executeImmediateOperation(t.Context(), tx, guid.ID(100+index), 90, "concurrent", input, operationBudget{100, 1000}, now.Add(time.Hour), now, next, operationAllow)
				return err
			})
		}()
	}
	group.Wait()
	for index := range workers {
		require.NoError(t, errors[index])
		require.Equal(t, results[0], results[index])
	}
	q := query.Bind(db, tree.prefix)
	count, err := q.Operation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	count, err = q.Version.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 8, count)
}

func TestImmediateRejectsStaleAndUnknownChoices(t *testing.T) {
	for _, scenario := range []string{"revision", "unknown", "duplicate", "invalid-policy"} {
		t.Run(scenario, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.FilePolicy = "ask"
			input := immediateOperationRequest{operationRequest: request}
			_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
			confirmImmediate(&input, immediateFrontier(t, err), "overwrite")
			want := errOperationInput
			switch scenario {
			case "revision":
				_, err := tree.createDirectory(t.Context(), db, 1, 60, 2, "Changed", time.Now())
				require.NoError(t, err)
				want = errTreeConflict
			case "unknown":
				input.Decisions[0].ConflictKey = strings.Repeat("a", 64)
				want = errTreeConflict
			case "duplicate":
				input.Decisions = append(input.Decisions, input.Decisions[0])
			case "invalid-policy":
				input.Decisions[0].Policy = "merge"
			}
			before := immediateDatabaseSnapshot(t, db, tree)
			_, err = executeImmediateFixture(t, db, tree, input, operationAllow)
			require.ErrorIs(t, err, want)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
		})
	}
}

func TestImmediateMoveAuthorizesDescendantsIncludingOldTrash(t *testing.T) {
	for _, oldTrash := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "old-trash"}[oldTrash], func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.Action, request.DirectoryPolicy = "move", "rename"
			if oldTrash {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return tree.trash(t.Context(), tx, 1, 70, 90, treeSpaceRevision(t, tx, tree.prefix, 1), []nodeRevision{{31, treeNode(t, tx, tree.prefix, 31).Version}}, 100, time.Now())
				}))
			}
			denied := errors.New("descendant move forbidden")
			before := immediateDatabaseSnapshot(t, db, tree)
			_, err := executeImmediateFixture(t, db, tree, immediateOperationRequest{operationRequest: request}, func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
				if id == 31 && action == "move" {
					return denied
				}
				return nil
			})
			require.ErrorIs(t, err, denied)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
		})
	}
}

func TestImmediateLateFailureRollsBackEverything(t *testing.T) {
	for _, scenario := range []string{"journal", "audit", "new-node-access", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.DirectoryPolicy = "replace"
			before := immediateDatabaseSnapshot(t, db, tree)
			budget := operationBudget{100, 1000}
			access := operationAccess(operationAllow)
			failure := errors.New("mandatory audit/access failure")
			switch scenario {
			case "journal":
				require.NoError(t, db.Exec("CREATE TRIGGER fail_immediate_journal BEFORE INSERT ON "+query.BindNames(tree.prefix).Operations.String()+" BEGIN SELECT RAISE(ABORT, 'late journal failure'); END").Error)
			case "new-node-access":
				access = func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
					if id > 10000 && action == "update" {
						return failure
					}
					return nil
				}
			case "budget":
				budget.Nodes = 5
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				now := time.Now()
				_, err := tree.executeImmediateOperation(t.Context(), tx, 100, 90, "immediate", immediateOperationRequest{operationRequest: request}, budget, now.Add(time.Hour), now, operationNext(t), access)
				if err != nil {
					return err
				}
				if scenario == "audit" {
					return failure
				}
				return nil
			})
			require.Error(t, err)
			require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
		})
	}
}

func TestImmediateReplayRechecksAccessAndRequiresTransaction(t *testing.T) {
	db, tree, request := newOperationTree(t)
	input := immediateOperationRequest{operationRequest: request}
	_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	require.NoError(t, err)
	before := immediateDatabaseSnapshot(t, db, tree)
	denied := errors.New("result access revoked")
	_, err = executeImmediateFixture(t, db, tree, input, func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
		if id == 20 && action == "read" {
			return denied
		}
		return nil
	})
	require.ErrorIs(t, err, denied)
	require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
	now := time.Now()
	_, err = tree.executeImmediateOperation(t.Context(), db, 101, 90, "no-tx", input, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, ErrAccountTransaction)
}

func TestImmediateNormalizesRootsAndPreservesOldTrashOnMove(t *testing.T) {
	for _, action := range []string{"copy", "move"} {
		t.Run(action, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.Action, request.DirectoryPolicy, request.Sources = action, "rename", []guid.ID{31, 11, 10, 10}
			if action == "move" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					return tree.trash(t.Context(), tx, 1, 70, 90, treeSpaceRevision(t, tx, tree.prefix, 1), []nodeRevision{{31, treeNode(t, tx, tree.prefix, 31).Version}}, 100, time.Now())
				}))
				// Trashed nodes cannot be explicitly selected. They remain part of
				// the physical subtree moved with the active ancestor.
				request.Sources = []guid.ID{11, 10, 10}
			}
			result, err := executeImmediateFixture(t, db, tree, immediateOperationRequest{operationRequest: request}, operationAllow)
			require.NoError(t, err)
			require.Len(t, result.Roots, 1)
			if action == "move" {
				require.Equal(t, 5, result.Moved)
				require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 31).State)
			} else {
				require.Equal(t, 5, result.Created)
			}
		})
	}
}

func TestImmediateTypeConflictRequiresExplicitReplacement(t *testing.T) {
	db, tree, request := newOperationTree(t)
	operationFixtureFile(t, db, tree, 50, 11, "Shared")
	request.Sources, request.TypePolicy = []guid.ID{50}, "ask"
	input := immediateOperationRequest{operationRequest: request}
	before := immediateDatabaseSnapshot(t, db, tree)
	_, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	frontier := immediateFrontier(t, err)
	require.Equal(t, "type", frontier.Conflicts[0].Kind)
	require.Equal(t, []string{"replace", "skip", "rename"}, frontier.Conflicts[0].Policies)
	require.Equal(t, before, immediateDatabaseSnapshot(t, db, tree))
	confirmImmediate(&input, frontier, "replace")
	result, err := executeImmediateFixture(t, db, tree, input, operationAllow)
	require.NoError(t, err)
	require.Equal(t, 4, result.Recycled)
	require.Equal(t, 1, result.Created)
	require.Equal(t, "trashed", treeNode(t, db, tree.prefix, 20).State)
}
