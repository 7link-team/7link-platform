package execution

import (
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentPerformanceStorageFailureCannotReportSuccessfulCount(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f := executionHarness(t)
		v := f.create(t)
		v.Plan.ExecutorPrincipalID = 21
		raw, e := json.Marshal(v.Plan)
		require.NoError(t, e)
		require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Updates(map[string]any{"plan_json": string(raw), "plan_hash": hash(raw)}).Error)
		return f, func(tx *gorm.DB) error {
			n, e := f.m.agentPerformanceRunningOn(f.human, tx, 7, 21)
			if e == nil {
				require.EqualValues(t, 1, n)
			}
			return e
		}
	})
}

func TestAgentPerformanceRejectsUnknownExecutorAndUnavailableVisibility(t *testing.T) {
	for _, fault := range []string{"transaction", "identity", "policy", "plan"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			failure := errors.New("visibility unavailable")
			if fault == "policy" {
				f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			}
			if fault == "plan" {
				require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("plan_json", "{").Error)
			}
			e := f.db.Transaction(func(tx *gorm.DB) error {
				if fault == "transaction" {
					tx = f.db
				}
				_, e := f.m.agentPerformanceRunningOn(f.human, tx, 7, 21)
				return e
			})
			switch fault {
			case "transaction":
				require.ErrorIs(t, e, ErrInvalidArgument)
			case "policy":
				require.ErrorIs(t, e, failure)
			default:
				require.ErrorIs(t, e, ErrCapabilityUnavailable)
			}
		})
	}
}

func TestAgentPerformanceRunningUsesFrozenExecutorCurrentVisibilityAndNonterminalState(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	plan := v.Plan
	plan.ExecutorPrincipalID = 777
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(raw), "state": "PAUSED"})
	require.NoError(t, err)
	check := func(want int64) {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			n, err := f.m.agentPerformanceRunningOn(f.human, tx, 7, 777)
			require.NoError(t, err)
			require.Equal(t, want, n)
			n, err = f.m.agentPerformanceRunningOn(f.human, tx, 7, 778)
			require.NoError(t, err)
			require.Zero(t, n)
			return nil
		}))
	}
	check(1)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.State, "INTERRUPTED")
	require.NoError(t, err)
	check(1)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.State, "SUCCEEDED")
	require.NoError(t, err)
	check(0)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.State, "RUNNING")
	require.NoError(t, err)
	f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	check(0)
	_, err = f.m.agentPerformanceRunningOn(f.human, f.db, 7, 777)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestBindAgentPerformanceRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindAgentPerformance(module.NewServices()), module.ErrServicesProviding)
}
