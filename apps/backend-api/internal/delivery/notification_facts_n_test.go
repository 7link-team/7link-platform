package delivery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationSourcesNeverPublishFactsAfterCanceledDatabaseReads(t *testing.T) {
	h := newResultFixture(t)
	claim, err := h.submit()
	require.NoError(t, err)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	readCtx, cancel := context.WithCancel(h.actor(12))
	cancel()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		fact, found, err := h.m.andonNotificationOn(readCtx, tx, 1, h.work)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		require.Zero(t, fact.OccurrenceID)
		_, found, err = h.m.andonNotificationOn(h.ctx, nil, 1, h.work)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		sheet, found, err := h.m.reviewSheetNotificationOn(readCtx, tx, 1, 777)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		require.Empty(t, sheet.Reviewers)
		_, found, err = h.m.reviewSheetNotificationOn(h.ctx, nil, 1, 777)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		eligible, err := h.m.reviewSheetNotificationEligibleOn(readCtx, tx, 1, 777, 12, "review_sheet.created")
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, eligible)
		lease, found, err := h.m.leaseExpiredNotificationOn(readCtx, tx, 1, 777)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		require.True(t, lease.EndedAt.IsZero())
		_, found, err = h.m.leaseExpiredNotificationOn(h.ctx, nil, 1, 777)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		id, project, found, err := h.m.humanVerificationNotificationOn(readCtx, tx, 1, h.work, 12)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		require.Zero(t, id)
		require.Zero(t, project)
		id, _, found, err = h.m.humanVerificationNotificationOn(h.ctx, tx, 1, 777, 12)
		require.NoError(t, err)
		require.False(t, found)
		require.Zero(t, id)
		_, _, found, err = h.m.humanVerificationNotificationOn(h.ctx, h.db, 1, h.work, 12)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		_, _, found, err = h.m.humanVerificationNotificationOn(h.ctx, nil, 1, h.work, 12)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		for _, kind := range []string{"plan", "work_item"} {
			decision, found, err := h.m.decisionNotificationFactsOn(readCtx, tx, 1, h.work, kind)
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, found)
			require.Zero(t, decision.WorkItemID)
		}
		pending, err := h.m.pendingHumanPlansOn(readCtx, tx, 1, 99)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, pending)
		err = h.m.heavyPlanningReferenceOn(readCtx, tx, 1, 99, h.work, 777)
		require.ErrorIs(t, err, context.Canceled)
		_, found, err = h.m.claimEvaluationOn(readCtx, tx, 1, 99, h.work, claim.ID)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		return nil
	}))
	for _, kind := range []string{"HUMAN_VERIFICATION", "ESCALATION_INTERNAL_EXHAUSTED", "PLAN_APPROVAL", "ANDON"} {
		req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, ProjectID: 99, Kind: kind, Limit: 10}
		var rows []governance.InboxItem
		var err error
		switch kind {
		case "PLAN_APPROVAL":
			rows, err = h.m.planApprovalInbox(readCtx, req)
		case "ANDON":
			rows, err = h.m.andonInbox(readCtx, req, nil)
		default:
			rows, err = h.m.verificationInboxWithLeads(readCtx, req, nil)
		}
		require.ErrorIs(t, err, context.Canceled, kind)
		require.Empty(t, rows, kind)
	}
	_, err = h.m.qualityReviewWindowOn(h.ctx, h.db, 1, 99, time.Now(), time.Now().Add(-time.Hour))
	require.ErrorIs(t, err, ErrInvalidArgument)
	var unavailable *Module
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := unavailable.decisionNotificationFactsOn(h.ctx, tx, 1, guid.ID(777), "plan")
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
}

func TestAttentionSourcesRequireCurrentTransactionAndPreserveMissingExecution(t *testing.T) {
	h := newResultFixture(t)
	canceled, cancel := context.WithCancel(h.ctx)
	cancel()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		fact, found, err := h.m.executionAttentionWorkOn(h.ctx, tx, 1, h.work)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, guid.ID(11), fact.ExecutorID)
		require.Equal(t, "HUMAN", fact.ExecutorKind)
		require.Zero(t, fact.ExecutionID, "Human work has no fabricated Runner execution")
		_, found, err = h.m.executionAttentionWorkOn(h.ctx, tx, 2, h.work)
		require.NoError(t, err)
		require.False(t, found)
		_, found, err = h.m.executionAttentionWorkOn(canceled, tx, 1, h.work)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		_, found, err = h.m.executionAttentionWorkOn(h.ctx, h.db, 1, h.work)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, found)
		for _, kind := range []string{"review_age", "project_blocked"} {
			_, found, err := h.m.deadlineNotificationOn(canceled, tx, 1, h.work, kind)
			require.ErrorIs(t, err, context.Canceled)
			require.False(t, found)
			_, found, err = h.m.deadlineNotificationOn(h.ctx, tx, 2, h.work, kind)
			require.NoError(t, err)
			require.False(t, found)
		}
		_, _, err = h.m.deadlineNotificationOn(h.ctx, nil, 1, h.work, "review_age")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, err = h.m.deadlineNotificationOn(h.ctx, tx, 1, h.work, "unknown")
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, err = h.m.constitutionNotificationEligibleOn(canceled, tx, 1, 777, 11)
		require.ErrorIs(t, err, context.Canceled)
		_, _, err = h.m.constitutionNotificationEligibleOn(h.ctx, nil, 1, 777, 11)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = h.m.acceptedUndeliveredOn(h.ctx, h.db, 1, 99, time.Now())
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = h.m.acceptedUndeliveredOn(canceled, tx, 1, 99, time.Now())
		require.ErrorIs(t, err, context.Canceled)
		_, err = h.m.attentionWorksOn(canceled, tx, 1, 99)
		require.ErrorIs(t, err, context.Canceled)
		_, err = h.m.attentionWorksOn(h.ctx, h.db, 1, 99)
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "EXECUTION_FAILED").Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		ids, err := h.m.attentionWorksOn(h.ctx, tx, 1, 99)
		require.NoError(t, err)
		require.Contains(t, ids, h.work)
		other, err := h.m.attentionWorksOn(h.ctx, tx, 1, 100)
		require.NoError(t, err)
		require.Empty(t, other)
		return nil
	}))
}

func TestStageReadyNoticesRequireCanonicalReadinessAndAtomicDeduplication(t *testing.T) {
	m, db, ctx, work := stageHarness(t)
	m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		require.Equal(t, "project.stage_ready", event.Type)
		require.Equal(t, "SYSTEM", event.Actor.Kind)
		data, ok := event.Data.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "DISCOVERY", data["target"])
		return 1, tx.Exec("INSERT INTO provision_probe(step) VALUES (?)", "event:"+event.Type).Error
	})
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "INTAKE").Error)
	scheduled := authn.WithClaims(ctx, &authn.Claims{TenantID: 1})
	history := events.ProjectOccurrences(func(context.Context, *gorm.DB, guid.ID, guid.ID, []string, time.Time) ([]events.ProjectOccurrence, error) {
		return nil, nil
	})
	invoke := func(callCtx context.Context, prior events.ProjectOccurrences) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.publishStageReadyOn(callCtx, tx, 1, 99, prior) })
	}
	require.ErrorIs(t, invoke(ctx, history), ErrWorkForbidden, "scheduled checks never borrow a Human identity")
	require.ErrorIs(t, invoke(scheduled, nil), ErrUnavailable)
	failure := errors.New("stage readiness history unavailable")
	require.ErrorIs(t, invoke(scheduled, func(context.Context, *gorm.DB, guid.ID, guid.ID, []string, time.Time) ([]events.ProjectOccurrence, error) {
		return nil, failure
	}), failure)
	var before int64
	require.NoError(t, db.Table("provision_probe").Count(&before).Error)
	prior := events.ProjectOccurrences(func(context.Context, *gorm.DB, guid.ID, guid.ID, []string, time.Time) ([]events.ProjectOccurrence, error) {
		return []events.ProjectOccurrence{{Target: "DISCOVERY"}}, nil
	})
	require.NoError(t, invoke(scheduled, prior))
	var after int64
	require.NoError(t, db.Table("provision_probe").Count(&after).Error)
	require.Equal(t, before, after, "same canonical target does not duplicate notification effects")
	auditFn := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, invoke(scheduled, history), failure)
	m.service.audit = auditFn
	require.NoError(t, invoke(scheduled, history))
	require.NoError(t, db.Table("provision_probe").Count(&after).Error)
	require.Greater(t, after, before)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, _, err := m.stageNotificationReadinessOn(canceled, tx, 1, 99)
		require.ErrorIs(t, err, context.Canceled)
		readable, err := m.notificationWorkReadableOn(ctx, tx, 1, work.ID, 11)
		require.NoError(t, err)
		require.True(t, readable)
		readable, err = m.notificationWorkReadableOn(ctx, tx, 1, 999999, 11)
		require.NoError(t, err)
		require.False(t, readable)
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
		readable, err = m.notificationWorkReadableOn(ctx, tx, 1, work.ID, 11)
		require.NoError(t, err)
		require.False(t, readable, "an existing notification source loses visibility immediately when current policy is revoked")
		return nil
	}))
}

func TestNotificationFactOutagesNeverBecomeMissingSourceSuccess(t *testing.T) {
	h := newResultFixture(t)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	canceled, cancel := context.WithCancel(h.ctx)
	cancel()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.cancelledDependentsOn(canceled, tx, 1, h.work)
		require.ErrorIs(t, err, context.Canceled)
		_, found, err := h.m.reviewSheetNotificationOn(canceled, tx, 1, 777)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		_, found, err = h.m.decisionNotificationFactsOn(canceled, tx, 1, h.work, "work_item")
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		_, _, found, err = h.m.humanVerificationNotificationOn(canceled, tx, 1, 777, 11)
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, found)
		err = h.m.heavyPlanningReferenceOn(canceled, tx, 1, 99, h.work, 777)
		require.ErrorIs(t, err, context.Canceled)
		_, err = h.m.qualityReviewWindowOn(canceled, tx, 1, 99, time.Now().Add(-time.Hour), time.Now())
		require.ErrorIs(t, err, context.Canceled)
		_, err = h.m.reportSuperiorOn(h.ctx, tx, 1, 11)
		require.ErrorIs(t, err, ErrUnavailable, "missing role resolver is not absence of a supervisor")
		return nil
	}))
	for _, scenario := range []string{"unavailable", "malformed", "owner"} {
		t.Run(scenario, func(t *testing.T) {
			failure := errors.New("current planning policy unavailable")
			h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "unavailable" {
					return authz.DataConstraint{}, failure
				}
				if scenario == "malformed" {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.heavyPlanningReferenceOn(h.ctx, tx, 1, 99, h.work, 777) })
			require.Error(t, err, "unknown policy or invisible work cannot validate a planning reference")
			if scenario == "unavailable" {
				require.ErrorIs(t, err, failure)
			}
		})
	}
}

// Every query in each observed successful provider read is a real dependency.
// Inject an outage at each position and require propagation, so a partial read
// cannot be reported as an empty successful fact to notifications/planning.
func TestProviderReadsFailClosedAtEveryObservedStorageRead(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	type fault struct{ at, count int }
	type key struct{}
	outage := errors.New("provider storage read unavailable")
	require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("regression_provider_outage", func(tx *gorm.DB) {
		if f, ok := tx.Statement.Context.Value(key{}).(*fault); ok {
			f.count++
			if f.at == f.count {
				tx.AddError(outage)
			}
		}
	}))
	defer h.db.Callback().Query().Remove("regression_provider_outage")
	start := time.Now().UTC().Add(-24 * time.Hour)
	end := start.Add(48 * time.Hour)
	calls := map[string]func(context.Context, *gorm.DB) error{
		"home": func(ctx context.Context, tx *gorm.DB) error {
			_, err := h.m.homeFactsOn(ctx, tx, 1, 99, start, end)
			return err
		},
		"planning": func(ctx context.Context, tx *gorm.DB) error {
			_, err := h.m.planningFactsOn(ctx, tx, 1, 99, 11, start, end)
			return err
		},
		"forecast": func(ctx context.Context, tx *gorm.DB) error {
			_, err := (clientWork{m: h.m}).forecastWorkOn(ctx, tx, 1, 99, nil, time.Now().UTC())
			return err
		},
		"pending_plans": func(ctx context.Context, tx *gorm.DB) error {
			_, err := h.m.pendingHumanPlansOn(ctx, tx, 1, 99)
			return err
		},
		"attention": func(ctx context.Context, tx *gorm.DB) error {
			_, _, err := h.m.executionAttentionWorkOn(ctx, tx, 1, h.work)
			return err
		},
		"human_check": func(ctx context.Context, tx *gorm.DB) error {
			_, _, _, err := h.m.humanVerificationNotificationOn(ctx, tx, 1, h.work, 12)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			probe := &fault{}
			ctx := context.WithValue(h.ctx, key{}, probe)
			require.NoError(t, h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return call(ctx, tx) }))
			require.Positive(t, probe.count)
			for at := 1; at <= probe.count; at++ {
				t.Run(fmt.Sprint(at), func(t *testing.T) {
					f := &fault{at: at}
					ctx := context.WithValue(h.ctx, key{}, f)
					err := h.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return call(ctx, tx) })
					require.ErrorIs(t, err, outage, "partial provider facts must never masquerade as successful emptiness")
				})
			}
		})
	}
}
