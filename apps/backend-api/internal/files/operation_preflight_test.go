package files

import (
	"context"
	"encoding/json"
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

func TestOperationPreparationChecksExistingTargetPermissions(t *testing.T) {
	for _, scenario := range []struct {
		name, policy, action string
		nodeID               guid.ID
	}{
		{"nested overwrite", "merge", "update", 40},
		{"nested directory", "merge", "create", 21},
		{"target visibility", "merge", "read", 20},
		{"replacement descendant", "replace", "delete", 41},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.DirectoryPolicy = scenario.policy
			before := treeSpaceRevision(t, db, tree.prefix, 1)
			denied := errors.New("target access denied")
			now := time.Now()
			_, err := tree.prepareOperation(t.Context(), db, 100, 90, "target-denied", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
				if id == scenario.nodeID && action == scenario.action {
					return denied
				}
				return nil
			})
			require.ErrorIs(t, err, denied)
			require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
			require.Equal(t, before, treeSpaceRevision(t, db, tree.prefix, 1))
			for _, id := range []guid.ID{10, 11, 20, 21, 30, 31, 32, 40, 41} {
				require.Equal(t, "active", treeNode(t, db, tree.prefix, id).State)
			}
			q := query.Bind(db, tree.prefix)
			versions, err := q.Version.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.EqualValues(t, 5, versions)
			pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
			require.NoError(t, err)
			require.Zero(t, pins, "failed preflight releases all prepared source pins")
		})
	}
}

func TestOperationReplacementPreflightPagesAndBounds(t *testing.T) {
	for _, scenario := range []string{"denied second page", "target budget", "success"} {
		t.Run(scenario, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			request.DirectoryPolicy = "replace"
			for index := 0; index < 101; index++ {
				_, err := tree.createDirectory(t.Context(), db, 1, guid.ID(300+index), 20, fmt.Sprintf("child-%d", index), time.Now())
				require.NoError(t, err)
			}
			before := treeSpaceRevision(t, db, tree.prefix, 1)
			budget := operationBudget{120, 2000}
			if scenario == "target budget" {
				budget.Nodes = 100
			}
			denied := errors.New("second page denied")
			deleteChecks := 0
			now := time.Now()
			op, err := tree.prepareOperation(t.Context(), db, 100, 90, "paged-target", request, budget, now.Add(time.Hour), now, operationNext(t), func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
				if action == "delete" {
					deleteChecks++
					if scenario == "denied second page" && id == 400 {
						return denied
					}
				}
				return nil
			})
			switch scenario {
			case "denied second page":
				require.ErrorIs(t, err, denied)
				require.Greater(t, deleteChecks, 100)
			case "target budget":
				require.ErrorIs(t, err, errHierarchy)
			case "success":
				require.NoError(t, err)
				require.Equal(t, "ready", op.State)
				require.Equal(t, 105, deleteChecks)
			}
			require.Equal(t, before, treeSpaceRevision(t, db, tree.prefix, 1))
			require.Equal(t, "active", treeNode(t, db, tree.prefix, 400).State)
		})
	}
}

func TestOperationSkippedTargetDoesNotRequireDeleteOrUpdate(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.DirectoryPolicy = "skip"
	now := time.Now()
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "skip-target", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, action string) error {
		if action == "update" || action == "delete" {
			return errors.New("unneeded target mutation permission")
		}
		return nil
	})
	require.NoError(t, err)
}

func TestOperationPreflightRejectsReplacementContainingAnotherSource(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.DirectoryPolicy = "replace"
	request.Sources = []guid.ID{10, 41}
	before := treeSpaceRevision(t, db, tree.prefix, 1)
	now := time.Now()
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "overlapping-replacement", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, errHierarchy)
	require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
	require.Equal(t, before, treeSpaceRevision(t, db, tree.prefix, 1))
	require.Equal(t, "active", treeNode(t, db, tree.prefix, 41).State)
	q := query.Bind(db, tree.prefix)
	pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
	require.NoError(t, err)
	require.Zero(t, pins)
}

func TestOperationPreflightRejectsSelfMerge(t *testing.T) {
	db, tree, request := newOperationTree(t)
	request.DestinationID = 3
	now := time.Now()
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "self-merge", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, errHierarchy)
	require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
	require.Equal(t, "active", treeNode(t, db, tree.prefix, 10).State)
}

func preparedCost(t *testing.T, operation *model.Operation) operationCost {
	t.Helper()
	var cost operationCost
	require.NoError(t, json.Unmarshal([]byte(operation.PlanJSON), &cost))
	require.Positive(t, cost.Nodes)
	return cost
}

func assertPlannedResult(t *testing.T, cost operationCost, result operationResult) {
	t.Helper()
	require.Equal(t, cost.Created, result.Created)
	require.Equal(t, cost.Moved, result.Moved)
	require.Equal(t, cost.Overwritten, result.Overwritten)
	require.Equal(t, cost.Skipped, result.Skipped)
	require.Equal(t, cost.Recycled, result.Recycled)
}

func TestOperationBatchFileReservationsMatchCommittedPolicies(t *testing.T) {
	for _, policy := range []string{"overwrite", "skip", "rename"} {
		t.Run(policy, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			operationFixtureFile(t, db, tree, 50, 3, "batch.txt")
			operationFixtureFile(t, db, tree, 60, 20, "BATCH.txt")
			request.Sources, request.FilePolicy = []guid.ID{60, 50}, policy
			before := treeSpaceRevision(t, db, tree.prefix, 1)
			now := time.Now()
			op, err := tree.prepareOperation(t.Context(), db, 100, 90, "batch-files", request, operationBudget{20, 200}, now.Add(time.Hour), now, operationNext(t), func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, _ string) error {
				require.Positive(t, int64(id), "prospective identities must never reach IAM")
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, before, treeSpaceRevision(t, db, tree.prefix, 1))
			cost := preparedCost(t, operationRecord(t, db, tree, op.ID))
			require.Equal(t, op.PlanJSON, operationRecord(t, db, tree, op.ID).PlanJSON)
			result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
			require.NoError(t, err)
			assertPlannedResult(t, cost, result)
			switch policy {
			case "overwrite":
				require.Equal(t, operationCost{Nodes: 3, ClosureRows: 3, Created: 1, Overwritten: 1}, cost)
			case "skip":
				require.Equal(t, operationCost{Nodes: 2, ClosureRows: 3, Created: 1, Skipped: 1}, cost)
			case "rename":
				require.Equal(t, operationCost{Nodes: 2, ClosureRows: 6, Created: 2}, cost)
				renamed, err := tree.resolvePath(t.Context(), db, 1, "/Destination/BATCH (1).txt")
				require.NoError(t, err)
				require.Equal(t, "BATCH (1).txt", renamed.Name)
			}
		})
	}
}

func TestOperationBatchVirtualDirectoryReservationsAndTypeReplacement(t *testing.T) {
	for _, policy := range []string{"merge", "replace", "skip", "rename", "type replace"} {
		t.Run(policy, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			_, err := tree.createDirectory(t.Context(), db, 1, 50, 3, "Batch", time.Now())
			require.NoError(t, err)
			operationFixtureFile(t, db, tree, 51, 50, "same.txt")
			if policy == "type replace" {
				operationFixtureFile(t, db, tree, 60, 20, "BATCH")
			} else {
				_, err := tree.createDirectory(t.Context(), db, 1, 60, 20, "BATCH", time.Now())
				require.NoError(t, err)
				operationFixtureFile(t, db, tree, 61, 60, "SAME.txt")
				operationFixtureFile(t, db, tree, 62, 60, "extra.txt")
				request.DirectoryPolicy = policy
			}
			request.Sources = []guid.ID{50, 60}
			op := prepareFixtureOperation(t, db, tree, 100, "virtual-directories", request, operationBudget{30, 300})
			cost := preparedCost(t, op)
			result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
			require.NoError(t, err)
			assertPlannedResult(t, cost, result)
			switch policy {
			case "merge":
				require.Equal(t, operationCost{Nodes: 6, ClosureRows: 11, Created: 3, Overwritten: 1}, cost)
			case "replace":
				require.Equal(t, operationCost{Nodes: 7, ClosureRows: 18, Created: 5, Recycled: 2}, cost)
			case "skip":
				require.Equal(t, operationCost{Nodes: 3, ClosureRows: 7, Created: 2, Skipped: 1}, cost)
			case "rename":
				require.Equal(t, operationCost{Nodes: 5, ClosureRows: 18, Created: 5}, cost)
			case "type replace":
				require.Equal(t, operationCost{Nodes: 5, ClosureRows: 10, Created: 3, Recycled: 2}, cost)
			}
		})
	}
}

func TestOperationPreparationRejectsCumulativeCostsWithoutTreeWrites(t *testing.T) {
	for _, scenario := range []string{"copy closures", "move closures", "replacement targets", "batch rename closures"} {
		t.Run(scenario, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			budget := operationBudget{100, 5}
			switch scenario {
			case "move closures":
				request.Action = "move"
			case "replacement targets":
				request.DirectoryPolicy = "replace"
				budget = operationBudget{10, 1000}
				for _, entry := range []struct {
					id, parent guid.ID
					name       string
				}{{50, 3, "A"}, {51, 50, "child"}, {60, 3, "B"}, {61, 60, "child"}, {70, 6, "A"}, {71, 70, "one"}, {72, 70, "two"}, {73, 70, "three"}, {80, 6, "B"}, {81, 80, "one"}, {82, 80, "two"}, {83, 80, "three"}} {
					_, err := tree.createDirectory(t.Context(), db, 1, entry.id, entry.parent, entry.name, time.Now())
					require.NoError(t, err)
				}
				request.Sources = []guid.ID{50, 60}
			case "batch rename closures":
				operationFixtureFile(t, db, tree, 50, 3, "batch.txt")
				operationFixtureFile(t, db, tree, 60, 20, "BATCH.txt")
				request.Sources, request.FilePolicy = []guid.ID{50, 60}, "rename"
			}
			q := query.Bind(db, tree.prefix)
			nodesBefore, err := q.Node.WithContext(t.Context()).Order(q.Node.ID.Asc()).Find()
			require.NoError(t, err)
			versionsBefore, err := q.Version.WithContext(t.Context()).Order(q.Version.ID.Asc()).Find()
			require.NoError(t, err)
			closuresBefore, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
			require.NoError(t, err)
			revision := treeSpaceRevision(t, db, tree.prefix, 1)
			now := time.Now()
			_, err = tree.prepareOperation(t.Context(), db, 100, 90, "cost-refusal", request, budget, now.Add(time.Hour), now, operationNext(t), operationAllow)
			require.ErrorIs(t, err, errHierarchy)
			require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
			require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
			nodesAfter, err := q.Node.WithContext(t.Context()).Order(q.Node.ID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, nodesBefore, nodesAfter)
			versionsAfter, err := q.Version.WithContext(t.Context()).Order(q.Version.ID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, versionsBefore, versionsAfter)
			closuresAfter, err := q.NodeClosure.WithContext(t.Context()).Order(q.NodeClosure.AncestorID.Asc(), q.NodeClosure.DescendantID.Asc()).Find()
			require.NoError(t, err)
			require.Equal(t, closuresBefore, closuresAfter)
			pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
			require.NoError(t, err)
			require.Zero(t, pins)
			trash, err := q.TrashBatch.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, trash)
		})
	}
}

func TestOperationPreparationRejectsMovedSelectedTarget(t *testing.T) {
	db, tree, request := newOperationTree(t)
	operationFixtureFile(t, db, tree, 50, 3, "batch.txt")
	operationFixtureFile(t, db, tree, 60, 20, "BATCH.txt")
	request.Sources, request.Action = []guid.ID{50, 60}, "move"
	now := time.Now()
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "moved-input-conflict", request, operationBudget{30, 300}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, errHierarchy)
	require.EqualValues(t, 3, *treeNode(t, db, tree.prefix, 50).ParentID)
	require.EqualValues(t, 20, *treeNode(t, db, tree.prefix, 60).ParentID)
}

func TestOperationCostMismatchRollsBackAndLegacyPlanFailsClosed(t *testing.T) {
	for _, plan := range []string{"{}", "invalid", `{"nodes":5,"closure_rows":9,"created":99}`} {
		t.Run(plan, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			op := prepareFixtureOperation(t, db, tree, 100, "cost-integrity", request, operationBudget{100, 1000})
			q := query.Bind(db, tree.prefix)
			_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Update(q.Operation.PlanJSON, plan)
			require.NoError(t, err)
			revision := treeSpaceRevision(t, db, tree.prefix, 1)
			nodesBefore, err := q.Node.WithContext(t.Context()).Count()
			require.NoError(t, err)
			versionsBefore, err := q.Version.WithContext(t.Context()).Count()
			require.NoError(t, err)
			_, err = executeFixtureOperation(t, db, tree, op, operationAllow)
			require.ErrorIs(t, err, errOperationState)
			require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
			nodesAfter, err := q.Node.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Equal(t, nodesBefore, nodesAfter)
			versionsAfter, err := q.Version.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Equal(t, versionsBefore, versionsAfter)
			require.Equal(t, "ready", operationRecord(t, db, tree, op.ID).State)
		})
	}
}

func TestOperationPreparationIgnoresNoOpChangedRowCounts(t *testing.T) {
	db, tree, request := newOperationTree(t)
	tables := query.BindNames(tree.prefix)
	require.NoError(t, db.Callback().Update().After("gorm:update").Register("test:no_op_zero_changed_rows", func(tx *gorm.DB) {
		if tx.Statement.Table == tables.Spaces.String() {
			tx.RowsAffected = 0
		}
	}))
	now := time.Now()
	op, err := tree.prepareOperation(t.Context(), db, 100, 90, "changed-row-portability", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.NoError(t, err, "a lock's no-op update may report zero changed rows")
	require.Equal(t, "ready", op.State)
	require.NoError(t, db.Callback().Update().Remove("test:no_op_zero_changed_rows"))
	_, err = executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	request.SpaceID = 999
	_, err = tree.prepareOperation(t.Context(), db, 101, 90, "missing-space", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, ErrNotFound, "zero changed rows must not authorize a missing space")
}

func TestOperationPreparationDepthIncludesOldTrashOnlyForMoves(t *testing.T) {
	for _, action := range []string{"copy", "move"} {
		t.Run(action, func(t *testing.T) {
			db, tree := newTrashTree(t)
			parent := guid.ID(6)
			for depth := 2; depth <= maxNodeDepth; depth++ {
				id := guid.ID(1000 + depth)
				_, err := tree.createDirectory(t.Context(), db, 1, id, parent, fmt.Sprintf("Level-%d", depth), time.Now())
				require.NoError(t, err)
				parent = id
			}
			require.NoError(t, trashSelection(t, db, tree, 200, 10, 5))
			request := operationRequest{SpaceID: 1, Action: action, DestinationID: guid.ID(1000 + maxNodeDepth - 2), Sources: []guid.ID{3}, FilePolicy: "overwrite", DirectoryPolicy: "merge", TypePolicy: "replace"}
			now := time.Now()
			revision := treeSpaceRevision(t, db, tree.prefix, 1)
			op, err := tree.prepareOperation(t.Context(), db, 100, 90, "depth-plan", request, operationBudget{100, 10000}, now.Add(time.Hour), now, operationNext(t), operationAllow)
			if action == "move" {
				require.ErrorIs(t, err, errHierarchy)
				require.Equal(t, revision, treeSpaceRevision(t, db, tree.prefix, 1))
			} else {
				require.NoError(t, err, "copy omits the deleted deepest descendant")
				result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
				require.NoError(t, err)
				assertPlannedResult(t, preparedCost(t, op), result)
			}
		})
	}
}

func TestOperationSimulationPagesSourcesAndCannotWriteTree(t *testing.T) {
	db, tree := newTrashTree(t)
	for index := range 105 {
		_, err := tree.createDirectory(t.Context(), db, 1, guid.ID(1000+index), 3, fmt.Sprintf("child-%d", index), time.Now())
		require.NoError(t, err)
	}
	tables := query.BindNames(tree.prefix)
	for _, register := range []func(string, func(*gorm.DB)) error{db.Callback().Create().Before("gorm:create").Register, db.Callback().Update().Before("gorm:update").Register, db.Callback().Delete().Before("gorm:delete").Register} {
		require.NoError(t, register("test:preparation_no_tree_writes", func(tx *gorm.DB) {
			for _, table := range []string{tables.Nodes.String(), tables.NodeClosure.String(), tables.Versions.String(), tables.TrashBatches.String(), tables.TrashRoots.String()} {
				if tx.Statement.Table == table {
					_ = tx.AddError(errors.New("preparation attempted to write user tree"))
				}
			}
		}))
	}
	request := operationRequest{SpaceID: 1, Action: "copy", DestinationID: 6, Sources: []guid.ID{3}, FilePolicy: "overwrite", DirectoryPolicy: "merge", TypePolicy: "replace"}
	op := prepareFixtureOperation(t, db, tree, 100, "paged-simulation", request, operationBudget{120, 1000})
	require.Equal(t, 108, preparedCost(t, op).Created)
	require.Equal(t, 108, op.ItemCount)
	require.NoError(t, db.Callback().Create().Remove("test:preparation_no_tree_writes"))
	require.NoError(t, db.Callback().Update().Remove("test:preparation_no_tree_writes"))
	require.NoError(t, db.Callback().Delete().Remove("test:preparation_no_tree_writes"))
	result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
	require.NoError(t, err)
	assertPlannedResult(t, preparedCost(t, op), result)
}

func TestOperationPreparationPropagatesStorageIndependentReadFailure(t *testing.T) {
	db, tree, request := newOperationTree(t)
	denied := errors.New("directory read failed")
	tables := query.BindNames(tree.prefix)
	// Install the read failure only after source pages are pinned, using the
	// destination read check made by the shared preflight walker.
	now := time.Now()
	checks := 0
	_, err := tree.prepareOperation(t.Context(), db, 100, 90, "read-failure", request, operationBudget{100, 1000}, now.Add(time.Hour), now, operationNext(t), func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, action string) error {
		if id == 6 && action == "create" {
			checks++
			if checks == 2 {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:preflight_target_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == tables.Nodes.String() {
						_ = tx.AddError(denied)
					}
				}))
			}
		}
		return nil
	})
	require.ErrorIs(t, err, denied)
	require.NoError(t, db.Callback().Query().Remove("test:preflight_target_read_failure"))
	require.Equal(t, "failed", operationRecord(t, db, tree, 100).State)
	q := query.Bind(db, tree.prefix)
	pins, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.PinnedNodeID.IsNotNull()).Count()
	require.NoError(t, err)
	require.Zero(t, pins)
}

func TestOperationSimulationUnknownProspectiveIdentityNeverQueriesDatabase(t *testing.T) {
	e := operationExecution{simulation: &operationSimulation{nodes: make(map[guid.ID]*model.Node), slots: make(map[operationSlot]guid.ID)}}
	// No database is supplied: a leaked prospective identity would panic rather
	// than return not found. Only real persisted identities may be queried.
	_, err := e.operationNode(-1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = e.operationTarget(-1, "unused")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOperationSimulationRememberClonesAndBoundsNodes(t *testing.T) {
	simulation := &operationSimulation{
		nodes:    make(map[guid.ID]*model.Node),
		slots:    make(map[operationSlot]guid.ID),
		children: make(map[guid.ID]map[guid.ID]bool),
		loaded:   make(map[operationSlot]bool),
	}
	execution := operationExecution{operation: &model.Operation{NodeBudget: 1}}
	parent := guid.ID(7)
	row := &model.Node{ID: -1, ParentID: &parent, Name: "original", NameKey: "original", State: "active"}
	require.NoError(t, simulation.remember(&execution, row))
	row.Name = "changed"
	require.Equal(t, "original", simulation.nodes[-1].Name)
	require.True(t, simulation.children[parent][-1])
	require.Equal(t, guid.ID(-1), simulation.slots[operationSlot{parent, "original"}])

	require.NoError(t, simulation.remember(&execution, &model.Node{ID: -1, Name: "replacement"}))
	require.Equal(t, "original", simulation.nodes[-1].Name, "remembering an existing identity must preserve the first snapshot")

	limit := 2*execution.operation.NodeBudget + maxNodeDepth + 1
	for id := 1; len(simulation.nodes) < limit; id++ {
		simulation.nodes[guid.ID(id)] = &model.Node{ID: guid.ID(id)}
	}
	require.ErrorIs(t, simulation.remember(&execution, &model.Node{ID: 999}), errHierarchy)
}

func TestOperationSimulationPathAndCacheFailures(t *testing.T) {
	simulation := &operationSimulation{
		nodes:    make(map[guid.ID]*model.Node),
		slots:    make(map[operationSlot]guid.ID),
		children: make(map[guid.ID]map[guid.ID]bool),
		loaded:   make(map[operationSlot]bool),
	}
	execution := operationExecution{simulation: simulation, operation: &model.Operation{NodeBudget: 100}}

	simulation.slots[operationSlot{-1, "missing"}] = 0
	_, err := execution.operationTarget(-1, "missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	simulation.nodes[-1] = &model.Node{ID: -1, State: "trashed"}
	_, err = execution.operationPath(-1)
	require.ErrorIs(t, err, ErrNotFound)

	self := guid.ID(-2)
	simulation.nodes[self] = &model.Node{ID: self, ParentID: &self, State: "active"}
	_, err = execution.operationPath(self)
	require.ErrorIs(t, err, errHierarchy)

	require.NoError(t, simulation.loadChildren(&execution, -3, true))
	require.Empty(t, simulation.loaded)
}

func TestOperationSimulationCancellationOrderingAndMutationGuards(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	simulation := &operationSimulation{
		nodes:    make(map[guid.ID]*model.Node),
		slots:    make(map[operationSlot]guid.ID),
		children: make(map[guid.ID]map[guid.ID]bool),
		loaded:   make(map[operationSlot]bool),
	}
	execution := operationExecution{ctx: ctx, simulation: simulation, operation: &model.Operation{NodeBudget: 10}}
	_, err := simulation.subtree(&execution, -1, false)
	require.ErrorIs(t, err, context.Canceled)

	require.True(t, prospectiveAfter(-2, 0))
	require.True(t, prospectiveAfter(-3, -2))
	require.False(t, prospectiveAfter(-2, -3))
	require.True(t, prospectiveAfter(3, 0))
	require.False(t, prospectiveAfter(3, -1))

	ctx = t.Context()
	execution.ctx = ctx
	parent := guid.ID(-1)
	source := &model.Node{ID: -1, ParentID: &parent, Kind: "directory", Name: "source", NameKey: "source", State: "active"}
	simulation.nodes[source.ID] = source
	err = simulation.move(&execution, source, source.ID, "source")
	require.ErrorIs(t, err, errHierarchy)
	require.Equal(t, &parent, source.ParentID)

	trashed := &model.Node{ID: -2, Kind: "file", State: "trashed"}
	simulation.nodes[trashed.ID] = trashed
	err = simulation.recycle(&execution, trashed.ID, false)
	require.ErrorIs(t, err, errHierarchy)
}
