package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHumanLeaseRecoveryGraceSuspensionReadinessAndReplay(t *testing.T) {
	for _, scenario := range []string{"ready", "draft", "within_grace", "at_grace", "suspended", "paused", "execution", "revoked_holder", "audit", "outbox"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, scenario != "draft")
			claimed := assignmentClaim(t, m, db, ctx, work, scenario == "draft")
			now := time.Now().UTC().Truncate(time.Millisecond)
			expired := now.Add(-61 * time.Second)
			if scenario == "within_grace" {
				expired = now.Add(-59 * time.Second)
			}
			if scenario == "at_grace" {
				expired = now.Add(-60 * time.Second)
			}
			require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id=?", claimed.Assignment.ID).Update("lease_expires_at", expired).Error)
			switch scenario {
			case "suspended":
				require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id=?", claimed.Assignment.ID).Update("lease_expires_at", nil).Error)
			case "paused":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("paused_at", now).Error)
			case "execution":
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 123).Error)
			case "revoked_holder":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, errors.New("outbox failed") })
			}
			m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return nil }
			err := m.reclaimHumanAssignments(ctx, 1, now)
			if scenario == "execution" || scenario == "audit" || scenario == "outbox" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			row, e := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			stored, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			if scenario == "ready" || scenario == "draft" || scenario == "revoked_holder" {
				require.Equal(t, "EXPIRED", row.Status)
				require.Nil(t, row.ActiveSlot)
				require.Equal(t, 2, row.Version)
				if scenario == "draft" {
					require.Equal(t, "DRAFT", stored.State)
					require.False(t, stored.ReadinessOK)
				} else {
					require.Equal(t, "READY", stored.State)
				}
				before := stored.Version
				require.NoError(t, m.reclaimHumanAssignments(ctx, 1, now.Add(time.Minute)))
				stored, e = query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
				require.NoError(t, e)
				require.Equal(t, before, stored.Version)
				tr := query.BindWorkTransitions(db, m.service.names)
				transition, e := tr.WithContext(ctx).Where(tr.ReasonCode.Eq("LEASE_EXPIRED")).First()
				require.NoError(t, e)
				require.Equal(t, "SYSTEM", transition.ActorKind)
				require.Nil(t, transition.ActorID)
			} else {
				require.Equal(t, "ACTIVE", row.Status)
				require.Equal(t, "CLAIMED", stored.State)
				require.Equal(t, claimed.WorkItem.Version, stored.Version)
			}
		})
	}
}

func TestLeaseJobDeclarationAndUnavailable(t *testing.T) {
	m := &Module{}
	declared := m.JobHandlers()
	require.Len(t, declared, 5)
	require.Equal(t, projectBudgetEnforceJob, declared[4].Kind)
	require.Error(t, declared[4].Run(t.Context(), jobs.Job{}))
	require.ErrorIs(t, declared[4].Run(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
	require.Equal(t, autonomyWatchJob, declared[2].Kind)
	declared = declared[1:]
	require.Equal(t, assignmentReclaimJob, declared[0].Kind)
	require.Error(t, declared[0].Run(t.Context(), jobs.Job{}))
	require.ErrorIs(t, declared[0].Run(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
	require.Equal(t, 30*time.Second, m.JobSchedules()[1].Interval)
	require.Equal(t, jobs.ScopePerTenant, m.JobSchedules()[1].TenantScope)
}

func TestLeaseFailureMetricsAndConsecutiveSweepsReset(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	assignmentClaim(t, m, db, ctx, work, false)
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("work_item_id=?", work.ID).Update("lease_expires_at", now.Add(-2*time.Minute)).Error)
	m.leaseFailures = prometheus.NewCounter(prometheus.CounterOpts{Name: "delivery_lease_reclaim_failures_total", Help: "Failed Human lease recovery attempts."})
	m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return errors.New("lock unavailable") }
	for round := 1; round <= 5; round++ {
		require.Error(t, m.reclaimHumanAssignments(ctx, 1, now))
		require.Equal(t, round, m.leaseFailureRounds[1])
	}
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(m.Collector()))
	metrics, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.InDelta(t, float64(5), metrics[0].Metric[0].Counter.GetValue(), 0)
	m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return nil }
	require.NoError(t, m.reclaimHumanAssignments(ctx, 1, now))
	require.Empty(t, m.leaseFailureRounds)
}

func TestLeaseRecoveryCapabilityFailuresRollBackOwnership(t *testing.T) {
	for _, scenario := range []string{"lock", "policy", "human", "member", "owner_changed", "holder_changed"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			claimed := assignmentClaim(t, m, db, ctx, work, false)
			now := time.Now().UTC().Truncate(time.Millisecond)
			q := query.BindAssignments(db, m.service.names)
			_, err := q.WithContext(ctx).Where(q.ID.Eq(claimed.Assignment.ID)).Update(q.LeaseExpiresAt, now.Add(-2*time.Minute))
			require.NoError(t, err)
			failure := errors.New("recovery capability unavailable")
			want := failure
			switch scenario {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, failure
				}
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, failure
				}
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure })
			case "owner_changed":
				w := query.BindWorkItems(db, m.service.names)
				_, err = w.WithContext(ctx).Where(w.ID.Eq(work.ID)).Update(w.AccountablePrincipalID, guid.ID(22))
				require.NoError(t, err)
				want = ErrWorkVersion
			case "holder_changed":
				_, err = q.WithContext(ctx).Where(q.ID.Eq(claimed.Assignment.ID)).Update(q.AssigneePrincipalID, guid.ID(22))
				require.NoError(t, err)
				want = ErrWorkVersion
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				return m.reclaimHumanAssignmentOn(ctx, tx, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, now)
			})
			require.ErrorIs(t, err, want)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(claimed.Assignment.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", row.Status)
			require.Equal(t, 1, row.Version)
			stored, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "CLAIMED", stored.State)
			require.Equal(t, claimed.WorkItem.Version, stored.Version)
		})
	}
}

func TestLeaseRecoveryRequiresExplicitTransactionAndValidIdentity(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	now := time.Now().UTC()
	require.ErrorIs(t, m.reclaimHumanAssignmentOn(ctx, db, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, now), ErrInvalidArgument)
	require.ErrorIs(t, m.reclaimHumanAssignmentOn(ctx, nil, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, now), ErrInvalidArgument)
	var absent *Module
	require.ErrorIs(t, absent.reclaimHumanAssignmentOn(ctx, nil, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, now), ErrUnavailable)
	row, err := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.Status)
	require.Equal(t, 1, row.Version)
}
