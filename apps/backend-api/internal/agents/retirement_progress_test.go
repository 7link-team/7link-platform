package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementHandoverProgressPersistsRealProducerTimeoutAndSingleRetry(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, _, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	m.now = func() time.Time { return now }
	ctx := agentContext(t, 2)
	var r model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = m.startRetirementOn(ctx, tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual responsibility"})
		return err
	}))
	launches, cancellations, summaries := 0, 0, 0
	producer := guid.ID(701)
	ready := false
	m.retirementHandover = RetirementHandover{
		LaunchOn: func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, objective string) (guid.ID, error) {
			launches++
			require.Equal(t, r.ID, id)
			require.Contains(t, objective, "## 在途工作项")
			require.Contains(t, objective, "## 给接手人的注意事项")
			require.NoError(t, tx.Exec("INSERT INTO audit_probe VALUES ('producer')").Error)
			return producer, nil
		},
		ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
			if !ready {
				return HandoverArtifact{}, ErrNotFound
			}
			return HandoverArtifact{ID: 804, ExecutionID: &producer, Source: "AGENT", Content: "actual model document", SHA256: "actual digest"}, nil
		},
		CancelOn: func(_ context.Context, _ *gorm.DB, _, _, _, execution guid.ID) error {
			cancellations++
			require.Equal(t, producer, execution)
			return nil
		},
		SystemOn: func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, summary string) (HandoverArtifact, error) {
			summaries++
			require.NotContains(t, summary, "## 给接手人的注意事项")
			return HandoverArtifact{ID: 803, Source: "SYSTEM", Content: summary, SHA256: "actual digest"}, nil
		},
	}
	advance := func(retry bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			next, err := m.advanceRetirementHandoverOn(ctx, tx, 1, r.ID, r.Version, retry)
			if err == nil {
				r = next
			}
			return err
		})
	}
	require.NoError(t, advance(false))
	require.Equal(t, &producer, r.Precheck.Progress.HandoverExecutionID)
	require.Equal(t, "HANDOVER", r.Step)
	version := r.Version
	require.NoError(t, advance(false))
	require.Equal(t, version, r.Version, "polling without progress must not change the protocol version")
	require.Equal(t, 1, launches)
	now = now.Add(handoverTimeout)
	require.NoError(t, advance(false))
	require.Equal(t, "SUCCESSOR", r.Step)
	require.Equal(t, "SYSTEM", r.Precheck.Progress.HandoverSource)
	require.Equal(t, 1, cancellations)
	require.Equal(t, 1, summaries)
	producer = 702
	require.NoError(t, advance(true))
	require.Equal(t, "HANDOVER", r.Step)
	require.Equal(t, 1, r.Precheck.Progress.HandoverRetryCount)
	require.Nil(t, r.Precheck.Progress.HandoverArtifactID)
	ready = true
	require.NoError(t, advance(false))
	require.Equal(t, "SUCCESSOR", r.Step)
	require.Equal(t, "AGENT", r.Precheck.Progress.HandoverSource)
	require.ErrorIs(t, advance(true), ErrConflict)
	require.Equal(t, 2, launches)
	q := query.BindRetirement(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, r.Precheck.Progress, stored.Precheck.Progress)
}

func TestRetirementHandoverProgressLaunchAuditFailureRollsBack(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, _, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	ctx := agentContext(t, 2)
	var r model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = m.startRetirementOn(ctx, tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual responsibility"})
		return err
	}))
	m.retirementHandover = RetirementHandover{
		LaunchOn: func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID, _ string) (guid.ID, error) {
			return 701, tx.Exec("INSERT INTO audit_probe VALUES ('producer')").Error
		},
		ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrNotFound
		},
		SystemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrUnavailable
		},
		CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil },
	}
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.advanceRetirementHandoverOn(ctx, tx, 1, r.ID, r.Version, false)
		return err
	}))
	var count int64
	require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "producer").Count(&count).Error)
	require.Zero(t, count)
	q := query.BindRetirement(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, r.Version, stored.Version)
	require.Nil(t, stored.Precheck.Progress)
}

func TestRetirementHandoverJobRechecksCurrentInitiatorAndPreservesDeniedProgress(t *testing.T) {
	m, db, _ := agentHarness(t)
	retirementWorkFixture(m)
	m.retirementRuntime.BeginOn = func(_ context.Context, _ *gorm.DB, _, agent, id guid.ID, version int) (fleet.AgentRetirementBinding, error) {
		return fleet.AgentRetirementBinding{PrincipalID: agent, RetirementID: id, State: "RETIRING", Version: version + 1}, nil
	}
	ctx := agentContext(t, 2)
	var r model.Retirement
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		r, err = m.startRetirementOn(ctx, tx, 1, 7, 5, RetirementStart{Mode: "NORMAL", Reason: "Transfer actual responsibility"})
		return err
	}))
	launches := 0
	m.retirementHandover = RetirementHandover{
		LaunchOn: func(ctx context.Context, _ *gorm.DB, tenant, _, _ guid.ID, _ string) (guid.ID, error) {
			launches++
			require.Equal(t, guid.ID(2), actorID(ctx))
			require.Equal(t, guid.ID(1), tenantID(ctx))
			require.Equal(t, guid.ID(1), tenant)
			return 701, nil
		},
		ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrNotFound
		},
		SystemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error) {
			return HandoverArtifact{}, ErrUnavailable
		},
		CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil },
	}
	policy := m.tenantPolicy
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	h := m.retirementJob()
	require.Error(t, h.Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Zero(t, launches)
	q := query.BindRetirement(db, m.names)
	stored, err := q.WithContext(ctx).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Nil(t, stored.Precheck.Progress)
	m.tenantPolicy = policy
	require.NoError(t, h.Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Equal(t, 1, launches)
	require.NoError(t, h.Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Equal(t, 1, launches, "recurring schedules must not launch duplicate producers")
	require.Error(t, h.Run(t.Context(), jobs.Job{TenantID: 0}))
}

func TestRetirementHandoverCurrentProducerAndSourceFailuresPreserveOriginalStep(t *testing.T) {
	for _, mode := range []string{"missing", "leads", "reconcile", "waiting", "launch", "zero-producer", "read", "system", "invalid-artifact"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			retirementWorkFixture(m)
			fault := errors.New("handover producer unavailable")
			agent := guid.ID(7)
			r := model.Retirement{ID: 900, TenantID: 1, AgentPrincipalID: agent, ActiveAgentID: &agent, Mode: "NORMAL", State: "IN_PROGRESS", Step: "HANDOVER", Reason: "Produce current source handover", AgentVersion: 6, Version: 1, CreatedBy: 2, CreatedAt: m.now(), StepStartedAt: m.now(), Precheck: model.RetirementPrecheck{Progress: &model.RetirementProgress{}}}
			if mode == "system" || mode == "invalid-artifact" {
				r.StepStartedAt = m.now().Add(-handoverTimeout)
				id := guid.ID(701)
				r.Precheck.Progress.HandoverExecutionID = &id
			}
			if mode == "read" {
				id := guid.ID(701)
				r.Precheck.Progress.HandoverExecutionID = &id
			}
			require.NoError(t, query.BindRetirement(db, m.names).WithContext(ctx).Create(&r))
			m.retirementHandover = RetirementHandover{LaunchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, error) {
				if mode == "launch" {
					return 0, fault
				}
				return 0, nil
			}, ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (HandoverArtifact, error) {
				if mode == "read" {
					return HandoverArtifact{}, fault
				}
				return HandoverArtifact{}, ErrNotFound
			}, CancelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }, SystemOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (HandoverArtifact, error) {
				if mode == "system" {
					return HandoverArtifact{}, fault
				}
				return HandoverArtifact{Source: "SYSTEM"}, nil
			}}
			switch mode {
			case "missing":
				m.retirementHandover.LaunchOn = nil
			case "leads":
				m.retirementWork.ClearLeadsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return delivery.ErrProjectForbidden }
			case "reconcile", "waiting":
				m.retirementHandover.ReconcileOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
					if mode == "reconcile" {
						return false, fault
					}
					return false, nil
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.advanceRetirementHandoverOn(ctx, tx, 1, r.ID, 1, false); return e })
			if mode == "waiting" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			q := query.BindRetirement(db, m.names)
			stored, e := q.WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, 1, stored.Version)
			require.Equal(t, "HANDOVER", stored.Step)
			require.Nil(t, stored.Precheck.Progress.HandoverArtifactID)
		})
	}
}
