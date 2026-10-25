package agents

import (
	"7link.com/platform/internal/iam/identity"
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func retirementWorkFixture(m *Module) {
	m.retirementWork.CheckReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.retirementExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error) {
		return []RetirementExecution{}, nil
	}
	m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
		return []delivery.RetirementAssignment{}, nil
	}
}

func TestRetirementForcedFreezesDisclosedLossAndProofRuntimeAuditAreAtomic(t *testing.T) {
	for _, fault := range []string{"", "name", "proof", "source", "runtime", "audit", "principal"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			retirementWorkFixture(m)
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				if fault == "source" {
					return nil, delivery.ErrUnavailable
				}
				return []delivery.RetirementAssignment{{AssignmentID: 90, ProjectID: 30, WorkItemID: 40, AssignmentVersion: 3, WorkVersion: 5}}, nil
			}
			m.retirementExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error) {
				return []RetirementExecution{{ID: 60, ProjectID: 30, State: "RUNNING", Version: 4}}, nil
			}
			m.forcedRetirementProof = func(_ context.Context, tx *gorm.DB, tenant, agent guid.ID, proof string) error {
				require.Equal(t, "fresh-forced-proof", proof)
				if fault == "proof" {
					return ErrForbidden
				}
				return tx.Exec("INSERT INTO audit_probe VALUES ('proof_consumed')").Error
			}
			calls := 0
			m.retirementRuntime.ForceOn = func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int, confirmName string, loss fleet.RetirementLoss) (fleet.AgentRetirementBinding, error) {
				if confirmName != "Test agent" {
					return fleet.AgentRetirementBinding{}, fleet.ErrRetirementNameMismatch
				}
				calls++
				require.Equal(t, fleet.RetirementLoss{OpenItems: 1, RunningExecutions: 1}, loss)
				if fault == "runtime" {
					return fleet.AgentRetirementBinding{}, fleet.ErrVersionConflict
				}
				if err := tx.Exec("INSERT INTO audit_probe VALUES ('forced_runtime')").Error; err != nil {
					return fleet.AgentRetirementBinding{}, err
				}
				return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRED", Version: version + 1}, nil
			}
			if fault == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error {
					return errors.New("forced audit unavailable")
				}
			}
			in := RetirementStart{Mode: "FORCED", Reason: "The retired machine cannot recover", ConfirmName: "Test agent", ReauthProof: "fresh-forced-proof"}
			if fault == "name" {
				in.ConfirmName = "test agent"
			}
			ctx := agentContext(t, 2)
			if fault == "principal" {
				ctx = agentContext(t, 7)
			}
			var r model.Retirement
			err := db.Transaction(func(tx *gorm.DB) error { var e error; r, e = m.startRetirementOn(ctx, tx, 1, 7, 5, in); return e })
			if fault != "" {
				require.Error(t, err)
				var count int64
				require.NoError(t, db.Table("audit_probe").Where("event_type IN ?", []string{"proof_consumed", "forced_runtime"}).Count(&count).Error)
				require.Zero(t, count, "proof and runtime mutation roll back together")
				count, e := query.BindRetirement(db, m.names).WithContext(t.Context()).Count()
				require.NoError(t, e)
				require.Zero(t, count)
				if fault == "proof" || fault == "principal" || fault == "source" {
					require.Zero(t, calls)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, "FORCED", r.Mode)
			require.Equal(t, "COMPLETED", r.State)
			require.Equal(t, "DONE", r.Step)
			require.Nil(t, r.ActiveAgentID)
			require.NotNil(t, r.CompletedAt)
			stored, e := query.BindRetirement(db, m.names).WithContext(t.Context()).Where(query.BindRetirement(db, m.names).ID.Eq(r.ID)).First()
			require.NoError(t, e)
			require.Equal(t, &model.RetirementLoss{OpenItems: 1, RunningExecutions: 1}, stored.Precheck.Lost)
		})
	}
}

func TestRetirementForcedCompletesExistingNormalRecordWithCurrentFences(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	m.forcedRetirementProof = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
	var original model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		original, e = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer the current responsibility"})
		return e
	}))
	m.retirementRuntime.ForceOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int, name string, loss fleet.RetirementLoss) (fleet.AgentRetirementBinding, error) {
		require.Equal(t, original.ID, id)
		if version != 8 {
			return fleet.AgentRetirementBinding{}, fleet.ErrVersionConflict
		}
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRED", Version: version + 1}, nil
	}
	force := RetirementStart{Mode: "FORCED", Reason: "Retire the machine after handover failure", ConfirmName: "Test agent", ReauthProof: "fresh"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 6, force); return e }), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 8, force); return e }))
	q := query.BindRetirement(db, m.names)
	rows, e := q.WithContext(t.Context()).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Equal(t, original.ID, r.ID)
	require.Equal(t, original.CreatedAt, r.CreatedAt)
	require.Equal(t, original.CreatedBy, r.CreatedBy)
	require.Equal(t, 2, r.Version)
	require.Equal(t, 9, r.AgentVersion)
	require.Equal(t, "FORCED", r.Mode)
	require.Equal(t, "COMPLETED", r.State)
	require.Nil(t, r.ActiveAgentID)
	require.NotNil(t, r.CompletedAt)
	require.NotNil(t, r.Precheck.Lost)
}

func TestRetirementPrecheckFreezesSourceManifestAndSourceFailurePreventsBegin(t *testing.T) {
	for _, sourceFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "durable_manifest", true: "unavailable_source"}[sourceFailure], func(t *testing.T) {
			m, db, _ := agentHarness(t)
			retirementWorkFixture(m)
			calls := 0
			m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				calls++
				return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
			}
			items := []delivery.RetirementAssignment{{AssignmentID: 90, ProjectID: 30, WorkItemID: 40, AssignmentVersion: 3, WorkVersion: 5, Title: "Transfer source work", State: "IN_PROGRESS"}}
			m.retirementWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.RetirementAssignment, error) {
				if sourceFailure {
					return nil, delivery.ErrUnavailable
				}
				return items, nil
			}
			var created model.Retirement
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				created, e = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual current ownership"})
				return e
			})
			if sourceFailure {
				require.ErrorIs(t, err, ErrUnavailable)
				require.Zero(t, calls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			items[0].Title = "Source has changed"
			items[0].WorkVersion = 6
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				r, e := m.retirementOn(agentContext(t, 2), tx, 1, created.ID, "agent_read", false)
				if e != nil {
					return e
				}
				require.Len(t, r.Precheck.WorkItems, 1)
				require.Equal(t, "Transfer source work", r.Precheck.WorkItems[0].Title)
				require.Equal(t, 5, r.Precheck.WorkItems[0].WorkVersion)
				require.Equal(t, 3, r.Precheck.WorkItems[0].AssignmentVersion)
				return nil
			}))
		})
	}
}

func TestRetirementDurableNormalStartCancelAndCurrentFences(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	beginCalls, cancelCalls := 0, 0
	m.retirementRuntime.BeginOn = func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		beginCalls++
		require.Equal(t, 5, version)
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: 6}, nil
	}
	m.retirementRuntime.CancelOn = func(_ context.Context, _ *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		cancelCalls++
		// Genuine ACKs may have advanced Fleet past the version persisted at start.
		if version != 8 {
			return fleet.AgentRetirementBinding{}, fleet.ErrVersionConflict
		}
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "STOPPED", Version: 9}, nil
	}
	var r model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer the Agent responsibilities"})
		return err
	}))
	require.Equal(t, "IN_PROGRESS", r.State)
	require.Equal(t, "HANDOVER", r.Step)
	require.Equal(t, 6, r.AgentVersion)
	require.Equal(t, 1, r.Version)
	// A fresh transaction reads the durable record, not a process-local map.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		read, err := m.retirementOn(agentContext(t, 2), tx, 1, r.ID, "agent_read", false)
		require.NoError(t, err)
		require.Equal(t, r.ID, read.ID)
		_, err = m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: r.Reason})
		require.ErrorIs(t, err, ErrConflict)
		_, err = m.cancelRetirementOn(agentContext(t, 2), tx, 1, r.ID, 2, 8)
		require.ErrorIs(t, err, ErrConflict)
		require.Zero(t, cancelCalls)
		_, err = m.cancelRetirementOn(agentContext(t, 2), tx, 1, r.ID, 1, 6)
		require.ErrorIs(t, err, ErrConflict)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		canceled, err := m.cancelRetirementOn(agentContext(t, 2), tx, 1, r.ID, 1, 8)
		require.NoError(t, err)
		require.Equal(t, "CANCELLED", canceled.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.Nil(t, canceled.ActiveAgentID)
		require.NotNil(t, canceled.CompletedAt)
		require.Equal(t, 9, canceled.AgentVersion)
		require.Equal(t, 2, canceled.Version)
		_, err = m.cancelRetirementOn(agentContext(t, 2), tx, 1, r.ID, 2, 9)
		require.ErrorIs(t, err, ErrConflict)
		return nil
	}))
	require.Equal(t, 1, beginCalls)
	require.Equal(t, 2, cancelCalls)
	// Terminal records release the active unique key but preserve their history.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: r.Reason})
		return err
	}))
}

func TestRetirementAuthorityUnavailableAndAuditRollback(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	start := RetirementStart{Mode: "NORMAL", Reason: "Transfer responsibility safely"}
	for _, actor := range []guid.ID{7, 8, 9} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.startRetirementOn(agentContext(t, actor), tx, 1, 7, 1, start)
			return err
		}), ErrForbidden)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 1, start)
		return err
	}), ErrUnavailable)
	m.retirementRuntime.BeginOn = func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		// Simulate an owner mutation inside the same caller transaction.
		if err := tx.Exec("INSERT INTO audit_probe VALUES ('runtime_begin')").Error; err != nil {
			return fleet.AgentRetirementBinding{}, err
		}
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	sentinel := errors.New("audit write failed")
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return sentinel }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 1, start)
		return err
	}), sentinel)
	var count int64
	var err error
	count, err = query.BindRetirement(db, m.names).WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "runtime_begin").Count(&count).Error)
	require.Zero(t, count)
}

func TestRetirementRejectsUnprovenForceInvalidReasonAndLateCancel(t *testing.T) {
	m, db, _ := agentHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, in := range []RetirementStart{{Mode: "NORMAL", Reason: "short"}, {Mode: "", Reason: "Sufficiently long reason"}, {Mode: "NOR", Reason: "Sufficiently long reason"}} {
			_, err := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 1, in)
			require.ErrorIs(t, err, ErrInvalid)
		}
		_, err := m.startRetirementOn(agentContext(t, 2), tx, 1, 7, 1, RetirementStart{Mode: "FORCED", Reason: "Sufficiently long reason"})
		require.ErrorIs(t, err, ErrForbidden)
		q := query.BindRetirement(tx, m.names)
		now := m.now()
		agent := guid.ID(7)
		for i, step := range []string{"REASSIGN", "CLEANUP", "REVOKE", "DONE"} {
			r := model.Retirement{ID: guid.ID(100 + i), TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: step, Reason: "Sufficiently long reason", Version: 1, AgentVersion: 1, CreatedBy: 2, CreatedAt: now, StepStartedAt: now}
			require.NoError(t, q.WithContext(t.Context()).Create(&r))
			_, err := m.cancelRetirementOn(agentContext(t, 2), tx, 1, r.ID, 1, 1)
			require.ErrorIs(t, err, ErrConflict)
			_, err = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).Delete()
			require.NoError(t, err)
		}
		return nil
	}))
}

func TestRetirementStartCurrentSourceFailuresLeaveNoProtocolOrRuntimeMutation(t *testing.T) {
	for _, mode := range []string{"missing-work", "missing-executions", "failed-executions", "failed-leads", "target-inactive", "target-error", "zero-id", "failed-id", "wrong-binding", "storage", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			retirementWorkFixture(m)
			ctx := agentContext(t, 2)
			fault := errors.New("retirement source unavailable")
			calls := 0
			m.retirementRuntime.BeginOn = func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				calls++
				require.NoError(t, tx.Exec("INSERT INTO audit_probe VALUES ('runtime-start')").Error)
				binding := fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, Version: version + 1, State: "RETIRING"}
				if mode == "wrong-binding" {
					binding.Version = version
				}
				return binding, nil
			}
			switch mode {
			case "missing-work":
				m.retirementWork.ListOn = nil
			case "missing-executions":
				m.retirementExecutions = nil
			case "failed-executions":
				m.retirementExecutions = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]RetirementExecution, error) { return nil, fault }
			case "failed-leads":
				m.retirementWork.LeadProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, fault }
			case "target-inactive", "target-error":
				original := m.principals.LockActiveRow
				m.principals.LockActiveRow = func(c context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 7 {
						if mode == "target-error" {
							return identity.Principal{}, false, fault
						}
						return identity.Principal{ID: id}, false, nil
					}
					return original(c, tx, id)
				}
			case "zero-id":
				m.ids = failedAgentIDs{id: 0}
			case "failed-id":
				m.ids = failedAgentIDs{err: fault}
			case "storage":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("retirement-storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Retirement.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("retirement-storage")
			case "outbox":
				m.events = badPublisher{}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.startRetirementOn(ctx, tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual responsibilities"})
				return e
			})
			require.Error(t, err)
			count, e := query.BindRetirement(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			var effects int64
			require.NoError(t, db.Table("audit_probe").Count(&effects).Error)
			require.Zero(t, effects)
			if mode != "wrong-binding" && mode != "storage" && mode != "outbox" {
				require.Zero(t, calls)
			}
		})
	}
}

func TestRetirementCancellationCurrentProducerAndStorageFailuresRemainInProgress(t *testing.T) {
	for _, mode := range []string{"missing-runtime", "missing-producer", "producer", "bad-binding", "write", "audit", "inactive-target", "target-source"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			agent, producer := guid.ID(7), guid.ID(701)
			fault := errors.New("cancel source unavailable")
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "HANDOVER", Reason: "Cancel actual retirement request", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{HandoverExecutionID: &producer}}}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.retirementRuntime.CancelOn = func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
				require.NoError(t, tx.Exec("INSERT INTO audit_probe VALUES ('cancel-runtime')").Error)
				return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "STOPPED", Version: version + 1}, nil
			}
			m.retirementHandover.CancelOn = func(_ context.Context, tx *gorm.DB, _, _, _, _ guid.ID) error {
				return tx.Exec("INSERT INTO audit_probe VALUES ('cancel-producer')").Error
			}
			switch mode {
			case "missing-runtime":
				m.retirementRuntime.CancelOn = nil
			case "missing-producer":
				m.retirementHandover.CancelOn = nil
			case "producer":
				m.retirementHandover.CancelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return fault }
			case "bad-binding":
				m.retirementRuntime.CancelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (fleet.AgentRetirementBinding, error) {
					return fleet.AgentRetirementBinding{State: "STOPPED"}, nil
				}
			case "write":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retirement-cancel", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Retirement.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Update().Remove("retirement-cancel")
			case "audit":
				m.audit = nil
			case "inactive-target", "target-source":
				lock := m.principals.LockActiveRow
				m.principals.LockActiveRow = func(c context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 7 {
						if mode == "target-source" {
							return identity.Principal{}, false, fault
						}
						return identity.Principal{ID: id}, false, nil
					}
					return lock(c, tx, id)
				}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.cancelRetirementOn(ctx, tx, 1, r.ID, 1, 6); return e }))
			q := query.BindRetirement(db, m.names)
			stored, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "IN_PROGRESS", stored.State)
			require.Equal(t, 1, stored.Version)
			var effects int64
			require.NoError(t, db.Table("audit_probe").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}
