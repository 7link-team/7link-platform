package files

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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
