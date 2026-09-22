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
)

func TestOperationExecutionRejectsInvalidRuntimeInputs(t *testing.T) {
	db, tree, _ := newOperationTree(t)
	now := time.Now()

	_, err := tree.executeOperation(t.Context(), nil, 1, 100, 90, 1, now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, errOperationInput)

	_, err = tree.executeOperation(t.Context(), db, 1, 100, 90, 1, now, operationNext(t), operationAllow)
	require.ErrorIs(t, err, ErrAccountTransaction)
}

func TestOperationExecutionIDAndNodeBudgetsFailClosed(t *testing.T) {
	want := errors.New("generator unavailable")
	execution := operationExecution{
		operation: &model.Operation{NodeBudget: 2},
		next: func() (int64, error) {
			return 0, want
		},
	}

	_, err := execution.newID()
	require.ErrorIs(t, err, errOperationInput)
	require.ErrorIs(t, err, want)

	execution.next = func() (int64, error) { return 0, nil }
	_, err = execution.newID()
	require.ErrorIs(t, err, errOperationInput)

	execution.next = func() (int64, error) { return 42, nil }
	id, err := execution.newID()
	require.NoError(t, err)
	require.Equal(t, guid.ID(42), id)

	require.ErrorIs(t, execution.consumeNodes(-1), errHierarchy)
	require.NoError(t, execution.consumeNodes(2))
	require.Equal(t, 2, execution.usedNodes)
	require.ErrorIs(t, execution.consumeNodes(1), errHierarchy)
}

func TestOperationExecutionRejectsCorruptedPreparedState(t *testing.T) {
	for _, scenario := range []string{"request json", "roots json", "duplicate root", "snapshot", "missing pin", "item count", "destination version"} {
		t.Run(scenario, func(t *testing.T) {
			db, tree, request := newOperationTree(t)
			op := prepareFixtureOperation(t, db, tree, 100, "corrupt-"+scenario, request, operationBudget{100, 1000})
			q := query.Bind(db, tree.prefix)

			switch scenario {
			case "request json":
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Update(q.Operation.RequestJSON, "{")
				require.NoError(t, err)
			case "roots json":
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Update(q.Operation.RootsJSON, "[")
				require.NoError(t, err)
			case "duplicate root":
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Update(q.Operation.RootsJSON, "[10,10]")
				require.NoError(t, err)
			case "snapshot":
				_, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.OperationID.Eq(op.ID), q.OperationItem.Ordinal.Eq(1)).Update(q.OperationItem.SnapshotJSON, "{}")
				require.NoError(t, err)
			case "missing pin":
				_, err := q.OperationItem.WithContext(t.Context()).Where(q.OperationItem.OperationID.Eq(op.ID), q.OperationItem.Ordinal.Eq(1)).UpdateSimple(q.OperationItem.PinnedNodeID.Null())
				require.NoError(t, err)
			case "item count":
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).UpdateSimple(q.Operation.ItemCount.Add(1))
				require.NoError(t, err)
			case "destination version":
				_, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).UpdateSimple(q.Operation.DestinationVersion.Add(1))
				require.NoError(t, err)
			}

			result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
			require.Error(t, err)
			require.Empty(t, result)
			require.Equal(t, "ready", operationRecord(t, db, tree, op.ID).State)
			switch scenario {
			case "snapshot", "destination version":
				require.ErrorIs(t, err, errTreeConflict)
			case "duplicate root":
				require.ErrorIs(t, err, errOperationInput)
			case "missing pin", "item count":
				require.ErrorIs(t, err, errOperationState)
			}
		})
	}
}

func TestOperationExecutionPropagatesStatementFailures(t *testing.T) {
	tables := []struct {
		name  string
		skips int64
	}{
		{"spaces", 5},
		{"operations", 6},
		{"operation items", 12},
		{"nodes", 22},
		{"closure", 16},
		{"versions", 10},
	}
	for _, table := range tables {
		for skip := int64(0); skip <= table.skips; skip++ {
			t.Run(fmt.Sprintf("%s/after_%d", table.name, skip), func(t *testing.T) {
				db, tree, request := newOperationTree(t)
				op := prepareFixtureOperation(t, db, tree, 100, fmt.Sprintf("fault-%s-%d", table.name, skip), request, operationBudget{100, 1000})
				names := query.BindNames(tree.prefix)
				var name string
				switch table.name {
				case "spaces":
					name = names.Spaces.String()
				case "operations":
					name = names.Operations.String()
				case "operation items":
					name = names.OperationItems.String()
				case "nodes":
					name = names.Nodes.String()
				case "closure":
					name = names.NodeClosure.String()
				case "versions":
					name = names.Versions.String()
				}
				fault := failTableAfter(t, db, name, skip)
				result, err := executeFixtureOperation(t, db, tree, op, operationAllow)
				if fault.Fired() {
					require.ErrorIs(t, err, errInjectedTableFault)
					require.Empty(t, result)
					return
				}
				require.NoError(t, err)
				require.NotEmpty(t, result.Roots)
			})
		}
	}
}
