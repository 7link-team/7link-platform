package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMeasuredForecastSourceAndPersistenceFailuresRollBackFacts(t *testing.T) {
	for _, fault := range []string{"work", "execution", "cost", "update", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _, _ := harness(t)
			tol := toleranceFixture(t, m, db, "PROJECT", 3)
			failure := errors.New("observed forecast source failed")
			m.forecastWork.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID, time.Time) (delivery.ForecastWorkMeasurement, error) {
				if fault == "work" {
					return delivery.ForecastWorkMeasurement{}, failure
				}
				spent := int64(0)
				return delivery.ForecastWorkMeasurement{SpentMicros: &spent, AcceptedWorkIDs: []guid.ID{7}, Samples: []delivery.ForecastWorkSample{{WorkItemID: 7, CycleMillis: 100}}}, nil
			}
			m.forecastExecutions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]execution.ForecastExecution, error) {
				if fault == "execution" {
					return nil, failure
				}
				return []execution.ForecastExecution{{ID: 8, WorkItemID: 7}}, nil
			}
			m.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				if fault == "cost" {
					return 0, false, failure
				}
				return 0, true, nil
			}
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
				if fault == "audit" {
					return failure
				}
				return nil
			}
			m.events = expiryPublisher{publish: func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
				if fault == "outbox" {
					return 0, failure
				}
				return 1, nil
			}}
			if fault == "update" {
				require.NoError(t, db.Exec("CREATE TRIGGER reject_forecast BEFORE UPDATE ON "+m.names.Tolerance.String()+" BEGIN SELECT RAISE(ABORT,'forecast update failed'); END").Error)
			}
			require.Error(t, m.forecastDue(t.Context(), 1, 3, now().Add(time.Hour)))
			q := query.BindTolerance(db, m.names)
			row, err := q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
			require.NoError(t, err)
			require.Equal(t, tol.Version, row.Version)
			require.Nil(t, row.ForecastAt)
			require.Nil(t, row.ForecastMeasurement)
			require.Nil(t, row.OpenExceptionID)
		})
	}
}

func TestRequirementForecastUsesOnlyKnownExecutionSettlements(t *testing.T) {
	for _, scenario := range []string{"known", "missing execution", "unknown settlement", "settlement error"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, _ := harness(t)
			tol := toleranceFixture(t, m, db, "REQUIREMENT", 10)
			m.forecastWork.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, requirement *guid.ID, _ time.Time) (delivery.ForecastWorkMeasurement, error) {
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 3, project)
				require.NotNil(t, requirement)
				require.EqualValues(t, 10, *requirement)
				return delivery.ForecastWorkMeasurement{WorkIDs: []guid.ID{7}, ScopeCriteria: 2}, nil
			}
			m.forecastExecutions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]execution.ForecastExecution, error) {
				if scenario == "missing execution" {
					return nil, nil
				}
				return []execution.ForecastExecution{{ID: 8, WorkItemID: 7}}, nil
			}
			m.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
				if scenario == "settlement error" {
					return 0, false, ErrUnavailable
				}
				return 4, scenario != "unknown settlement", nil
			}
			err := m.forecastDue(t.Context(), 1, 3, now().Add(time.Hour))
			q := query.BindTolerance(db, m.names)
			row, readErr := q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
			require.NoError(t, readErr)
			if scenario == "settlement error" {
				require.ErrorIs(t, err, ErrUnavailable)
				require.Nil(t, row.ForecastAt)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, row.ForecastAt)
			require.EqualValues(t, 2, *row.ForecastScopeCriteria)
			if scenario == "known" {
				require.EqualValues(t, 4, *row.ForecastCostMicros)
			} else {
				require.Nil(t, row.ForecastCostMicros)
			}
		})
	}
}

func TestForecastServicesRejectEachMissingOwnerCapability(t *testing.T) {
	for missing := 0; missing < 5; missing++ {
		t.Run([]string{"work", "executions", "settlements", "queue", "subscribers"}[missing], func(t *testing.T) {
			s := module.NewServices()
			if missing > 0 {
				require.NoError(t, module.Provide[delivery.ForecastWorkFacts](s, delivery.ForecastWorkFacts{}))
			}
			if missing > 1 {
				require.NoError(t, module.Provide[execution.ForecastFacts](s, execution.ForecastFacts{}))
			}
			if missing > 2 {
				require.NoError(t, module.Provide[budget.SettlementFacts](s, budget.SettlementFacts{}))
			}
			if missing > 3 {
				require.NoError(t, module.Provide[jobs.Queue](s, &forecastQueueProbe{}))
			}
			require.NoError(t, s.Seal())
			require.Error(t, (&Module{}).injectForecastServices(s))
		})
	}
}
