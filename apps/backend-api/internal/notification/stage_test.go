package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStageSuggestionSourceAndStorageFailuresCannotCreateNotice(t *testing.T) {
	for _, mode := range []string{"source", "candidates", "order", "visibility", "lock", "guard", "read", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.decisionFacts.StageSuggestionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) {
				if mode == "source" {
					return "", ErrUnavailable
				}
				return "BUILD", nil
			}
			switch mode {
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("stage_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.notifyStageSuggestionOn(t.Context(), tx, 7, 81) }))
			if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Remove("stage_read"))
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestStageSuggestionJobRejectsInvalidAndUnavailableCurrentSource(t *testing.T) {
	for _, mode := range []string{"tenant", "missing", "database", "payload", "occurrences_missing", "occurrences", "projects", "order", "publish"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			published := 0
			f.m.decisionFacts.StageProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error) {
				if mode == "projects" {
					return nil, ErrUnavailable
				}
				if mode == "order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{81}, nil
			}
			f.m.decisionFacts.PublishStageReadyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { published++; return ErrUnavailable }
			job := jobs.Job{TenantID: 7}
			switch mode {
			case "tenant":
				job.TenantID = 0
			case "missing":
				f.m.decisionFacts.StageProjectsOn = nil
			case "database":
				f.m.runtime = nil
			case "payload":
				job.Payload = []byte(`{"project_id":"bad"}`)
			case "occurrences_missing":
				job.Payload = []byte(`{"project_id":"81"}`)
				f.m.projectOccurrences = nil
			case "occurrences":
				job.Payload = []byte(`{"project_id":"81"}`)
				f.m.projectOccurrences = func(context.Context, *gorm.DB, guid.ID, guid.ID, []string, time.Time) ([]events.ProjectOccurrence, error) {
					return nil, ErrUnavailable
				}
			}
			require.Error(t, f.m.stageSuggestionJob(t.Context(), job))
			if mode == "publish" {
				require.Equal(t, 1, published)
			} else {
				require.Zero(t, published)
			}
		})
	}
}

func TestStageHealthDebouncesCurrentEventsAndScanPublishesSource(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	f.m.now = func() time.Time { return now }
	published := 0
	f.m.decisionFacts.StageProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error) { return []guid.ID{9}, nil }
	f.m.decisionFacts.PublishStageReadyOn = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID) error {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 9, project)
		published++
		return nil
	}
	require.NoError(t, f.m.stageSuggestionJob(t.Context(), jobs.Job{TenantID: 7}))
	require.Equal(t, 1, published)
	last := now.Add(-3 * time.Second)
	f.m.projectOccurrences = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, types []string, since time.Time) ([]events.ProjectOccurrence, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 9, project)
		require.Equal(t, stageHealthEventTypes, types)
		if !last.Before(since) {
			return []events.ProjectOccurrence{{At: last}}, nil
		}
		return nil, nil
	}
	job := jobs.Job{TenantID: 7, Payload: []byte(`{"project_id":"9"}`)}
	require.Error(t, f.m.stageSuggestionJob(t.Context(), job), "new source event extends the quiet period")
	require.Equal(t, 1, published)
	now = now.Add(7 * time.Second)
	require.NoError(t, f.m.stageSuggestionJob(t.Context(), job))
	require.Equal(t, 2, published)
	require.Error(t, f.m.stageSuggestionJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{"project_id":"0"}`)}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.stageHealthEventOn(t.Context(), tx, events.RecordedEvent{ID: 101, TenantID: 7, ProjectID: 9})
	}))
	requests := f.m.queue.(*fixtureQueue).requests
	require.NotEmpty(t, requests)
	require.Equal(t, now.Add(10*time.Second), requests[len(requests)-1].RunAt)
}

func TestStageSuggestionRechecksCanonicalReadinessAndRollingDay(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	f.m.now = func() time.Time { return now }
	target := "ACTIVE"
	f.m.decisionFacts.StageProjectsOn = func(_ context.Context, _ *gorm.DB, tenant, after guid.ID, _ int) ([]guid.ID, error) {
		require.EqualValues(t, 7, tenant)
		if after == 0 {
			return []guid.ID{9}, nil
		}
		return nil, nil
	}
	f.m.decisionFacts.StageSuggestionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return target, nil }
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.PrincipalID == 12, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
	}
	run := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			return f.m.projectEventOn(t.Context(), tx, events.RecordedEvent{ID: 77, TenantID: 7, ProjectID: 9, SubjectID: 9, Type: "project.stage_ready", Data: json.RawMessage(`{"target":"ACTIVE"}`)})
		}))
	}
	run()
	run()
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("project.stage_suggestion")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	now = now.Add(23 * time.Hour)
	run()
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	now = now.Add(time.Hour)
	run()
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	target = ""
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible, "changed guards or stage resolve the suggestion")
	require.Equal(t, "OFF", defaultEmail(rows[0].Type, Info))
}
