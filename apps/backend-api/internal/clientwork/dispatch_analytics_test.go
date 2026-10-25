package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestDispatchAnalyticsDoesNotInventHistoryOrCostWhenProvidersFail(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	at := now()
	ex, claim := guid.ID(101), guid.ID(1)
	work := delivery.DispatchHistoryWork{ProjectID: 3, WorkItemID: 11, Claims: []delivery.DispatchClaimFact{{ID: claim, ExecutionID: &ex, CreatedAt: at.Add(-time.Hour)}}, Decisions: []delivery.DispatchDecisionFact{{Action: "APPROVE", CreatedAt: at}}, AcceptedAt: &at, AcceptedClaimID: &claim}
	failure := errors.New("analytics source failed")
	for _, stage := range []string{"missing", "history_error", "execution_error", "wrong_work", "accepted_wrong_work", "no_accepted_claim", "cost_missing", "cost_error", "overflow"} {
		t.Run(stage, func(t *testing.T) {
			copyModule := *m
			works := []delivery.DispatchHistoryWork{work}
			copyModule.dispatchDecisions.ReadOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) ([]delivery.DispatchHistoryWork, error) {
				return works, nil
			}
			copyModule.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
				return []execution.DispatchExecutionFact{{ID: ex, WorkItemID: 11, ExecutorPrincipalID: 21}}, nil
			}
			copyModule.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				return 100, true, nil
			}
			want := error(nil)
			switch stage {
			case "missing":
				copyModule.dispatchDecisions.ReadOn = nil
				want = ErrUnavailable
			case "history_error":
				copyModule.dispatchDecisions.ReadOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) ([]delivery.DispatchHistoryWork, error) {
					return nil, failure
				}
				want = failure
			case "execution_error":
				copyModule.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
					return nil, failure
				}
				want = failure
			case "wrong_work", "accepted_wrong_work":
				copyModule.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
					return []execution.DispatchExecutionFact{{ID: ex, WorkItemID: 99, ExecutorPrincipalID: 21}}, nil
				}
				want = ErrUnavailable
				if stage == "accepted_wrong_work" {
					works[0].Decisions = nil
				}
			case "no_accepted_claim":
				unknown := guid.ID(99)
				works[0].AcceptedClaimID = &unknown
			case "cost_missing":
				copyModule.forecastCosts.ExecutionsOn = nil
			case "cost_error":
				copyModule.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
					return 0, false, failure
				}
				want = failure
			case "overflow":
				works = append(works, work)
				copyModule.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
					return math.MaxInt64, true, nil
				}
				want = ErrUnavailable
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				stats, a, n, err := copyModule.measuredDispatchOn(ctx, tx, 1, "TASK", at)
				if want != nil {
					require.ErrorIs(t, err, want)
					require.Nil(t, stats)
					require.Zero(t, a)
					require.Zero(t, n)
				} else {
					require.NoError(t, err)
					if stage == "cost_missing" {
						require.False(t, stats[21].CostAvailable)
						require.Nil(t, stats[21].CPAMicros)
					}
					if stage == "no_accepted_claim" {
						require.Zero(t, stats[21].AcceptedItems)
					}
				}
				return nil
			}))
		})
	}
	for _, providerError := range []error{agents.ErrForbidden, agents.ErrUnavailable, failure} {
		m.dispatchCatalog.ForAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.SkillCatalogSnapshot, error) {
			return agents.SkillCatalogSnapshot{}, providerError
		}
		out := DispatchSnapshot{Candidates: []DispatchCandidate{{DispatchAgentFact: fleet.DispatchAgentFact{PrincipalID: 21}}}, Limitations: []string{"SKILL_CATALOG_UNAVAILABLE"}}
		err := m.enrichDispatchAnalyticsOn(ctx, db, 1, 3, &out)
		if errors.Is(providerError, failure) {
			require.ErrorIs(t, err, failure)
		} else {
			require.NoError(t, err)
		}
		require.Contains(t, out.Limitations, "SKILL_CATALOG_UNAVAILABLE")
		require.Nil(t, out.Candidates[0].Catalog)
	}
}

func TestDispatchAnalyticsCountsFirstDecisionsAndAllAcceptedCostBranches(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	at := now()
	first := at.Add(-10 * time.Hour)
	second := at.Add(-5 * time.Hour)
	accepted := at.Add(-time.Hour)
	ex1, ex2, ex3 := guid.ID(101), guid.ID(102), guid.ID(201)
	claim2, claim3 := guid.ID(2), guid.ID(3)
	m.dispatchDecisions.ReadOn = func(context.Context, *gorm.DB, guid.ID, string, time.Time) ([]delivery.DispatchHistoryWork, error) {
		return []delivery.DispatchHistoryWork{
			{ProjectID: 3, WorkItemID: 11, Claims: []delivery.DispatchClaimFact{{ID: 1, ActorID: 999, ExecutionID: &ex1, CreatedAt: first.Add(-time.Hour)}, {ID: 2, ActorID: 999, ExecutionID: &ex2, CreatedAt: second}}, Decisions: []delivery.DispatchDecisionFact{{ID: 10, ClaimID: 1, Action: "REQUEST_CHANGES", CreatedAt: first}, {ID: 11, ClaimID: 2, Action: "APPROVE", CreatedAt: accepted}}, AcceptedClaimID: &claim2, AcceptedAt: &accepted},
			{ProjectID: 3, WorkItemID: 12, Claims: []delivery.DispatchClaimFact{{ID: 3, ActorID: 999, ExecutionID: &ex3, CreatedAt: first.Add(-time.Hour)}}, Decisions: []delivery.DispatchDecisionFact{{ID: 20, ClaimID: 3, Action: "APPROVE", CreatedAt: accepted}}, AcceptedClaimID: &claim3, AcceptedAt: &accepted},
			// This is a Human result. The initiator/actor ID must not become Agent data.
			{ProjectID: 3, WorkItemID: 13, Claims: []delivery.DispatchClaimFact{{ID: 4, ActorID: 21, CreatedAt: first}}, Decisions: []delivery.DispatchDecisionFact{{ID: 30, ClaimID: 4, Action: "APPROVE", CreatedAt: accepted}}},
		}, nil
	}
	m.dispatchExecutions.ForWorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time) ([]execution.DispatchExecutionFact, error) {
		return []execution.DispatchExecutionFact{{ID: 101, WorkItemID: 11, ExecutorPrincipalID: 21}, {ID: 102, WorkItemID: 11, ExecutorPrincipalID: 21}, {ID: 103, WorkItemID: 11, ExecutorPrincipalID: 21, BranchReason: "RETRY_INFRA"}, {ID: 201, WorkItemID: 12, ExecutorPrincipalID: 22}}, nil
	}
	m.forecastCosts.ExecutionsOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID) (int64, bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		if ids[0] == 101 {
			require.Equal(t, []guid.ID{101, 102, 103}, ids)
			return 30000, true, nil
		}
		require.Equal(t, []guid.ID{201}, ids)
		return 20000, true, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		stats, a, n, err := m.measuredDispatchOn(testContext(t, 2), tx, 1, "TASK", at)
		require.NoError(t, err)
		require.EqualValues(t, 2, a)
		require.EqualValues(t, 3, n)
		require.EqualValues(t, 1, stats[21].FirstDecisions)
		require.Zero(t, stats[21].FirstPassAccepts)
		require.EqualValues(t, 1, stats[21].AcceptedItems)
		require.EqualValues(t, 30000, *stats[21].CPAMicros)
		require.EqualValues(t, 1, stats[22].FirstPassAccepts)
		require.EqualValues(t, 20000, *stats[22].CPAMicros)
		require.NotContains(t, stats, guid.ID(999))
		require.Equal(t, 5333, dispatchMeasuredFPA(0, 1, a, n))
		require.Equal(t, 7333, dispatchMeasuredFPA(1, 1, a, n))
		out := DispatchSnapshot{Work: delivery.DispatchWorkFacts{Type: "TASK"}, Candidates: []DispatchCandidate{{DispatchAgentFact: fleet.DispatchAgentFact{PrincipalID: 21}}, {DispatchAgentFact: fleet.DispatchAgentFact{PrincipalID: 22}}}, Limitations: []string{"QUALITY_COST_HISTORY_UNAVAILABLE"}}
		require.NoError(t, m.enrichDispatchAnalyticsOn(testContext(t, 2), tx, 1, 3, &out))
		require.True(t, out.AnalyticsAvailable)
		require.Equal(t, "AUTHORIZED_WORK_ITEMS", out.AnalyticsScope)
		require.Equal(t, 0, out.Candidates[0].Components.CostBP)
		require.Equal(t, 10000, out.Candidates[1].Components.CostBP)
		m.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
			return 0, false, nil
		}
		stats, _, _, err = m.measuredDispatchOn(testContext(t, 2), tx, 1, "TASK", at)
		require.NoError(t, err)
		require.Nil(t, stats[21].CPAMicros)
		require.False(t, stats[21].CostAvailable)
		require.Nil(t, stats[21].CostOnAcceptedMicros)
		return nil
	}))
	require.Equal(t, 5000, dispatchMeasuredFPA(0, 0, 0, 0))
	require.Equal(t, 5000, dispatchCostBP(math.MaxInt64/2, 0, math.MaxInt64))
	require.Equal(t, 10000, dispatchCostBP(20, 20, 20))
}
