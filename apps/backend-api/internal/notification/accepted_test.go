package notification

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAcceptedSummaryRollingDayAudienceAndCurrentClosure(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	active := true
	mode := "HYBRID"
	f.m.now = func() time.Time { return now }
	f.m.workFacts.ProjectModeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return mode, nil }
	f.m.decisionFacts.AcceptedUndeliveredOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]guid.ID, error) {
		if active {
			return []guid.ID{91, 92}, nil
		}
		return nil, nil
	}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 11 || r.Permission == "project_takeover", Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10", SourceType: "entity_binding", ScopeID: r.EntityID.String()}}}, nil
	}
	run := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.acceptedSummaryOn(t.Context(), tx, 7, 81) }))
	}
	run()
	run()
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(acceptedUndeliveredType)).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	require.Contains(t, rows[0].Params, "READY_SENT_MEMBERSHIP_UNAVAILABLE")
	now = now.Add(24*time.Hour - time.Millisecond)
	run()
	n, err := q.WithContext(t.Context()).Where(q.Type.Eq(acceptedUndeliveredType)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	now = now.Add(time.Millisecond)
	mode = "AUTONOMOUS"
	run()
	n, err = q.WithContext(t.Context()).Where(q.Type.Eq(acceptedUndeliveredType)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	active = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestAcceptedSummaryScheduledSourcesAndFailuresDoNotInventDeliveryFacts(t *testing.T) {
	for _, mode := range []string{"success", "projects", "project_order", "source", "candidates", "candidate_order", "guard", "read", "insert", "missing", "database"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.decisionFacts.StageProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error) {
				if mode == "projects" {
					return nil, ErrUnavailable
				}
				if mode == "project_order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{81}, nil
			}
			f.m.decisionFacts.AcceptedUndeliveredOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]guid.ID, error) {
				if mode == "source" {
					return nil, ErrUnavailable
				}
				return []guid.ID{91}, nil
			}
			f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "candidate_order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{11}, nil
			}
			if mode == "guard" {
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			}
			if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("accepted_summary_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if mode == "insert" {
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("accepted_summary_insert", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			if mode == "missing" {
				f.m.decisionFacts.AcceptedUndeliveredOn = nil
			}
			if mode == "database" {
				f.m.runtime = nil
			}
			err := f.m.acceptedSummaryJob(t.Context(), jobs.Job{TenantID: 7})
			require.NoError(t, f.db.Callback().Query().Remove("accepted_summary_read"))
			require.NoError(t, f.db.Callback().Create().Remove("accepted_summary_insert"))
			q := query.BindNotifications(f.db, f.m.names)
			rows, e := q.WithContext(t.Context()).Find()
			require.NoError(t, e)
			if mode == "success" {
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Contains(t, rows[0].Params, `"source_count":"1"`)
				require.NoError(t, f.m.acceptedSummaryJob(t.Context(), jobs.Job{TenantID: 7}))
				n, e := q.WithContext(t.Context()).Count()
				require.NoError(t, e)
				require.EqualValues(t, 1, n)
			} else {
				require.Error(t, err)
				require.Empty(t, rows)
			}
		})
	}
	f := newFixture(t)
	require.Error(t, f.m.acceptedSummaryJob(t.Context(), jobs.Job{}))
}
