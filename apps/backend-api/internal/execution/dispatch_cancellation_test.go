package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchConnectorBindsActualExecutionCapabilitiesAndPropagatesFailure(t *testing.T) {
	for _, scenario := range []string{"missing", "invalid", "bind_error", "bound"} {
		t.Run(scenario, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("dispatch binding failed")
			var bound delivery.DispatchExecutionAccess
			if scenario != "missing" {
				c := delivery.DispatchExecutionConnector{}
				if scenario != "invalid" {
					c.Bind = func(access delivery.DispatchExecutionAccess) error {
						bound = access
						if scenario == "bind_error" {
							return failure
						}
						return nil
					}
				}
				require.NoError(t, module.Provide[delivery.DispatchExecutionConnector](s, c))
			}
			require.NoError(t, s.Seal())
			err := f.m.bindDispatch(s)
			if scenario == "invalid" {
				require.ErrorIs(t, err, ErrCapabilityUnavailable)
			} else if scenario == "bind_error" {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			if scenario == "bound" {
				require.NotNil(t, bound.PrincipalsOn)
				require.NotNil(t, bound.StopOn)
				facts, err := bound.QueuesOn(f.human, f.db, 7, []guid.ID{1})
				require.NoError(t, err)
				require.Len(t, facts, 1)
				require.Zero(t, facts[0].PendingCommands)
			}
		})
	}
}

func TestDispatchStopDiscoveryAndCancellationErrorsPreserveExecution(t *testing.T) {
	for _, scenario := range []string{"nil_db", "tenant", "missing_row", "wrong_work", "capability", "machine_error", "principals_error", "query", "stale_version", "outside_transaction"} {
		t.Run(scenario, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			before := f.read(t, v.ID)
			failure := errors.New("dispatch dependency failed")
			f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) {
				if scenario == "principals_error" {
					return nil, failure
				}
				return []guid.ID{11}, nil
			}
			if scenario == "capability" {
				f.m.work.PrincipalsOn = nil
			}
			if scenario == "machine_error" {
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			}
			if scenario == "query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("dispatch_stop_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Executions.String() {
						tx.AddError(failure)
					}
				}))
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if scenario == "stale_version" {
					_, err := f.m.dispatchStopOn(f.human, tx, 7, v.ProjectID, v.WorkItemID, v.ID, v.Version+1)
					return err
				}
				if scenario == "outside_transaction" {
					_, err := f.m.dispatchStopOn(f.human, f.db, 7, v.ProjectID, v.WorkItemID, v.ID, v.Version)
					return err
				}
				tenant, work, id := guid.ID(7), v.WorkItemID, v.ID
				if scenario == "nil_db" {
					tx = nil
				}
				if scenario == "tenant" {
					tenant = 8
				}
				if scenario == "wrong_work" {
					work++
				}
				if scenario == "missing_row" {
					id++
				}
				_, err := f.m.dispatchStopPrincipalsOn(f.human, tx, tenant, v.ProjectID, work, id)
				return err
			})
			require.Error(t, err)
			if scenario == "query" {
				require.NoError(t, f.db.Callback().Query().Remove("dispatch_stop_failure"))
			}
			require.Equal(t, before, f.read(t, v.ID), "failed discovery/cancellation must leave canonical execution unchanged")
		})
	}
}

func TestForecastFactsUseEarliestBoundedServerReceiptsAndFailClosed(t *testing.T) {
	for _, scenario := range []string{"receipts", "execution_query", "start_query", "end_query"} {
		t.Run(scenario, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			at := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
			start, end := at.Add(-30*time.Minute), at.Add(-20*time.Minute)
			for i, receipt := range []model.Receipt{
				{Kind: "ACCEPTED", CreatedAt: start.Add(time.Minute)},
				{Kind: "ACCEPTED", CreatedAt: start},
				{Kind: "COMPLETED", CreatedAt: end},
				{Kind: "FAILED", CreatedAt: end.Add(time.Minute)},
				{Kind: "ACCEPTED", CreatedAt: at.Add(time.Minute)},
			} {
				receipt.ID, receipt.TenantID, receipt.ExecutionID, receipt.DispatchEpoch, receipt.Seq = guid.ID(9000+i), 7, v.ID, 1, int64(i+1)
				receipt.State, receipt.ContentHash = "RUNNING", "server-observed-hash"
				require.NoError(t, query.BindReceipts(f.db, f.m.names).WithContext(f.human).Create(&receipt))
			}
			failure := errors.New("forecast query unavailable")
			receiptQueries := 0
			require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("forecast_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == f.m.names.Executions.String() && scenario == "execution_query" {
					tx.AddError(failure)
				}
				if tx.Statement.Table == f.m.names.Receipts.String() {
					receiptQueries++
					if (scenario == "start_query" && receiptQueries == 1) || (scenario == "end_query" && receiptQueries == 2) {
						tx.AddError(failure)
					}
				}
			}))
			err := f.db.Transaction(func(tx *gorm.DB) error {
				facts, err := f.m.forecastExecutionFactsOn(f.human, tx, 7, v.ProjectID, at)
				if scenario == "receipts" {
					require.NoError(t, err)
					require.Len(t, facts, 1)
					require.Equal(t, &start, facts[0].StartedAt)
					require.Equal(t, &end, facts[0].EndedAt)
					require.True(t, facts[0].Terminal)
				} else {
					require.Nil(t, facts, "query failure must not become partial forecast observations")
				}
				return err
			})
			if scenario == "receipts" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.forecastExecutionFactsOn(f.human, tx, 7, v.ProjectID, time.Time{})
				return err
			}), ErrInvalidArgument)
		})
	}
}

func TestForecastFactsUseServerAcceptedReceiptsAndKeepQueuedStartUnknown(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	read := func() []ForecastExecution {
		var out []ForecastExecution
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.forecastExecutionFactsOn(t.Context(), tx, 7, v.ProjectID, time.Now().UTC().Add(time.Minute))
			return e
		}))
		return out
	}
	rows := read()
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].StartedAt)
	f.pull(t, v.ID)
	_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, e)
	rows = read()
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].StartedAt)
	require.Equal(t, v.WorkItemID, rows[0].WorkItemID)
	require.False(t, rows[0].Terminal)
	_, e = f.m.forecastExecutionFactsOn(t.Context(), nil, 7, v.ProjectID, time.Now())
	require.Error(t, e)
}

func TestDispatchStopUsesExactExecutionAndWaitsForRunnerAcknowledgement(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "unleased", true: "running"}[running], func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return []guid.ID{11}, nil }
			v := f.create(t)
			if running {
				f.pull(t, v.ID)
				_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, err)
			}
			before := f.read(t, v.ID)
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := f.m.dispatchStopOn(f.human, tx, 7, v.ProjectID, v.WorkItemID+1, v.ID, before.Version)
				return err
			}), ErrNotFound)
			require.Equal(t, before, f.read(t, v.ID))
			var stopped delivery.DispatchExecutionState
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				ids, err := f.m.dispatchStopPrincipalsOn(f.human, tx, 7, v.ProjectID, v.WorkItemID, v.ID)
				require.NoError(t, err)
				require.Contains(t, ids, guid.ID(11))
				stopped, err = f.m.dispatchStopOn(f.human, tx, 7, v.ProjectID, v.WorkItemID, v.ID, before.Version)
				return err
			}))
			require.NotNil(t, stopped.CancelRequestedAt)
			if running {
				require.Equal(t, "RUNNING", stopped.State)
				v, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: StateCanceled})
				require.NoError(t, err)
				require.Equal(t, StateCanceled, v.State)
			} else {
				require.Equal(t, StateCanceled, stopped.State)
			}
		})
	}
}

func TestDispatchQueueCountsOnlyUndeliveredAutostartCommands(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	_, err := q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.Autostart, true)
	require.NoError(t, err)
	read := func() int {
		t.Helper()
		var n int
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			facts, err := f.m.dispatchQueuesOn(f.human, tx, 7, []guid.ID{v.MachineID, v.MachineID})
			require.NoError(t, err)
			require.Len(t, facts, 1)
			n = facts[0].PendingCommands
			return err
		}))
		return n
	}
	require.Equal(t, 1, read())
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.Autostart, false)
	require.NoError(t, err)
	require.Zero(t, read())
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.Autostart, true)
	require.NoError(t, err)
	at := time.Now().UTC()
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.LeaseExpiresAt, &at)
	require.NoError(t, err)
	require.Zero(t, read())
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).UpdateSimple(q.LeaseExpiresAt.Null(), q.CancelRequestedAt.Value(at))
	require.NoError(t, err)
	require.Zero(t, read())
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.dispatchQueuesOn(f.human, tx, 8, []guid.ID{v.MachineID})
		return err
	}), ErrInvalidArgument)
}

func TestProjectCancellationUsesCanonicalIntentAndRunnerFences(t *testing.T) {
	for _, leased := range []bool{false, true} {
		t.Run(map[bool]string{false: "unleased", true: "running"}[leased], func(t *testing.T) {
			f := executionHarness(t)
			f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) { return []guid.ID{11}, nil }
			v := f.create(t)
			if leased {
				f.pull(t, v.ID)
				_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
				require.NoError(t, e)
			}
			services := module.NewServices()
			var hooks delivery.ProjectExecutionCancellation
			receiptReads := 0
			require.NoError(t, module.Provide[delivery.ProjectCancellationConnector](services, delivery.ProjectCancellationConnector{
				Bind: func(c delivery.ProjectExecutionCancellation) error { hooks = c; return nil },
				AuthorizeReceiptOn: func(_ context.Context, _ *gorm.DB, l delivery.ExecutionWorkLink) error {
					require.Equal(t, v.ID, l.ExecutionID)
					require.Equal(t, v.WorkItemID, l.WorkItemID)
					receiptReads++
					return nil
				},
			}))
			require.NoError(t, services.Seal())
			require.NoError(t, f.m.bindCancellation(services))
			before := f.read(t, v.ID)
			sentinel := errors.New("caller rollback")
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
				ids, e := hooks.PrincipalsOn(f.human, tx, 7, v.ProjectID)
				require.NoError(t, e)
				require.Contains(t, ids, guid.ID(11))
				if e = hooks.CancelOn(f.human, tx, 7, v.ProjectID); e != nil {
					return e
				}
				return sentinel
			}), sentinel)
			require.Equal(t, before, f.read(t, v.ID))
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return hooks.CancelOn(f.human, tx, 7, v.ProjectID) }))
			current := f.read(t, v.ID)
			require.NotNil(t, current.CancelRequestedAt)
			if !leased {
				require.Equal(t, StateCanceled, current.State)
				require.Equal(t, "CONFIRMED", current.ResultState)
				return
			}
			require.Equal(t, "RUNNING", current.State)
			// Ordinary work authorization is now denied, as it is after KILL.
			// Only executions with persisted cancel intent use the stop adapter.
			f.authorized = false
			cancel := f.pull(t, v.ID)
			require.NotNil(t, cancel.CancelRequestedAt)
			r, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: StateCanceled})
			require.NoError(t, e)
			require.Equal(t, StateCanceled, r.State)
			require.GreaterOrEqual(t, receiptReads, 2)
		})
	}
}

func TestProjectCancellationDiscoveryFailsClosed(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	_, e := f.m.projectCancellationPrincipalsOn(f.human, f.db, 8, v.ProjectID)
	require.Error(t, e)
	f.m.machines.ByIDOn = nil
	_, e = f.m.projectCancellationPrincipalsOn(f.human, f.db, 7, v.ProjectID)
	require.ErrorIs(t, e, ErrCapabilityUnavailable)
	require.ErrorIs(t, f.m.cancelProjectExecutionsOn(f.human, f.db, 7, v.ProjectID), ErrInvalidArgument)
}

func TestProjectCancellationConnectorAndDiscoveryRejectMissingFacts(t *testing.T) {
	for _, fault := range []string{"no_provider", "bad_connector", "bind_error", "query", "machine", "principals", "nil_transaction"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			v := f.create(t)
			failure := errors.New("cancellation source failed")
			if fault == "no_provider" || fault == "bad_connector" || fault == "bind_error" {
				s := module.NewServices()
				if fault != "no_provider" {
					c := delivery.ProjectCancellationConnector{}
					if fault == "bind_error" {
						c.Bind = func(delivery.ProjectExecutionCancellation) error { return failure }
						c.AuthorizeReceiptOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink) error { return nil }
					}
					require.NoError(t, module.Provide[delivery.ProjectCancellationConnector](s, c))
				}
				require.NoError(t, s.Seal())
				e := f.m.bindCancellation(s)
				if fault == "no_provider" {
					require.NoError(t, e)
				} else {
					require.Error(t, e)
				}
				return
			}
			f.m.work.PrincipalsOn = func(context.Context, *gorm.DB, WorkLink) ([]guid.ID, error) {
				if fault == "principals" {
					return nil, failure
				}
				return []guid.ID{11}, nil
			}
			if fault == "machine" {
				f.m.machines.ByIDOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, failure
				}
			}
			if fault == "query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("cancel_discovery_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Executions.String() {
						tx.AddError(failure)
					}
				}))
			}
			if fault == "nil_transaction" {
				require.ErrorIs(t, f.m.cancelProjectExecutionsOn(f.human, nil, 7, v.ProjectID), ErrInvalidArgument)
				return
			}
			_, e := f.m.projectCancellationPrincipalsOn(f.human, f.db, 7, v.ProjectID)
			require.Error(t, e)
		})
	}
}

func TestBindCancellationRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindCancellation(module.NewServices()), module.ErrServicesProviding)
}

func TestBindDispatchRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindDispatch(module.NewServices()), module.ErrServicesProviding)
}

func TestProvideForecastFactsRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideForecastFacts(s))
	require.ErrorIs(t, f.m.provideForecastFacts(s), module.ErrServiceDuplicate)
}
