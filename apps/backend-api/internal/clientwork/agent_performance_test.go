package clientwork

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentPerformanceHistoryIndependentWindowsRealOwnerReworkAndAllAcceptedBranchCosts(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	at := now()
	old := at.Add(-45 * 24 * time.Hour)
	recent := at.Add(-time.Hour)
	ex1, ex2, claim := guid.ID(101), guid.ID(102), guid.ID(2)
	m.dispatchDecisions.ReadOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, kind string, _ time.Time) ([]delivery.DispatchHistoryWork, error) {
		require.Equal(t, "TASK", kind)
		return []delivery.DispatchHistoryWork{{ProjectID: 3, WorkItemID: 11, Claims: []delivery.DispatchClaimFact{{ID: 1, ActorID: 999, ExecutionID: &ex1, CreatedAt: old.Add(-time.Hour)}, {ID: 2, ActorID: 999, ExecutionID: &ex2, CreatedAt: recent.Add(-time.Minute)}, {ID: 3, ActorID: 21, CreatedAt: recent}}, Decisions: []delivery.DispatchDecisionFact{{ID: 1, ClaimID: 1, Action: "REQUEST_CHANGES", CreatedAt: old}, {ID: 2, ClaimID: 1, Action: "REJECT", CreatedAt: recent.Add(-time.Minute)}, {ID: 3, ClaimID: 2, Action: "APPROVE", CreatedAt: recent}}, AcceptedAt: &recent, AcceptedClaimID: &claim}}, nil
	}
	m.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
		return []execution.DispatchExecutionFact{{ID: 101, WorkItemID: 11, AssignmentID: 1, ExecutorPrincipalID: 21}, {ID: 102, WorkItemID: 11, AssignmentID: 1, ExecutorPrincipalID: 21}, {ID: 103, WorkItemID: 11, AssignmentID: 1, ExecutorPrincipalID: 21, BranchReason: "RETRY_INFRA"}, {ID: 104, WorkItemID: 11, ExecutorPrincipalID: 22}, {ID: 105, WorkItemID: 11, ExecutorPrincipalID: 21, BranchReason: "SKILL_DISTILLATION"}}, nil
	}
	m.forecastCosts.ExecutionsOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
		if len(ids) == 4 {
			require.Equal(t, []guid.ID{101, 102, 103, 105}, ids)
			return 999999, true, nil
		}
		require.Equal(t, []guid.ID{101, 102, 103}, ids)
		return 606, true, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.agentPerformanceHistoryOn(testContext(t, 2), tx, 1, 21, 30, "TASK", at)
		require.NoError(t, err)
		require.Len(t, v, 1)
		require.Zero(t, v[0].FirstDecisions)
		require.EqualValues(t, 1, v[0].Claims)
		require.EqualValues(t, 1, v[0].ReworkDecisions)
		require.EqualValues(t, 1, v[0].AcceptedItems)
		require.EqualValues(t, 606, *v[0].CostOnAcceptedMicros)
		v, err = m.agentPerformanceHistoryOn(testContext(t, 2), tx, 1, 21, 90, "TASK", at)
		require.NoError(t, err)
		require.EqualValues(t, 1, v[0].FirstDecisions)
		require.Zero(t, v[0].FirstPassAccepts)
		require.EqualValues(t, 2, v[0].Claims)
		require.EqualValues(t, 2, v[0].ReworkDecisions)
		v, err = m.agentPerformanceHistoryOn(testContext(t, 2), tx, 1, 999, 90, "TASK", at)
		require.NoError(t, err)
		require.Zero(t, v[0].Claims)
		require.Zero(t, v[0].AcceptedItems)
		return nil
	}))
}
