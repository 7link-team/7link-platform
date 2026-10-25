package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutonomyWatchEachCurrentPrerequisiteLosesAndRecoversExactlyOnce(t *testing.T) {
	for _, condition := range []string{"P1", "P2", "P3", "P4"} {
		t.Run(condition, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			original := `[{"kind":"ESCALATION","source":"original","extra":{"amount":12}}]`
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "holds": original}).Error)
			failed := true
			m.modeGovernance.PreconditionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
				if failed && condition == "P2" {
					return true, []string{"BUDGET"}, nil
				}
				return !(failed && condition == "P1"), nil, nil
			}
			m.modeGovernance.AgentRoleOn = func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, role string) (bool, error) {
				return !(failed && condition == "P4" && role == "agent_executor"), nil
			}
			if condition == "P3" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("budget_state", "EXHAUSTED").Error)
			}
			require.NoError(t, db.Exec("CREATE TABLE autonomy_effects(kind TEXT NOT NULL, detail TEXT NOT NULL)").Error)
			m.assignmentGuard = func(_ context.Context, _ *gorm.DB, key string) error {
				require.Equal(t, "delivery:autonomy:1", key)
				return nil
			}
			m.service.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
				require.Zero(t, e.PrincipalID)
				return tx.Exec("INSERT INTO autonomy_effects VALUES ('audit',?)", e.EventType).Error
			}
			m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
				require.Equal(t, "SYSTEM", e.Actor.Kind)
				require.Zero(t, e.Actor.ID)
				raw, err := json.Marshal(e.Data)
				require.NoError(t, err)
				return 1, tx.Exec("INSERT INTO autonomy_effects VALUES (?,?)", e.Type, string(raw)).Error
			})
			for range 2 {
				require.NoError(t, m.watchAutonomy(ctx, 1, 99))
			}
			q := query.BindProjects(db, m.service.names)
			p, err := q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.Equal(t, "AUTONOMOUS", p.TeamMode)
			require.Equal(t, 2, p.Version)
			var holds []map[string]any
			require.NoError(t, json.Unmarshal([]byte(p.Holds), &holds))
			require.Len(t, holds, 2)
			require.Equal(t, "ESCALATION", holds[0]["kind"])
			require.Equal(t, "original", holds[0]["source"])
			require.Equal(t, autonomyHold, holds[1]["kind"])
			var count int64
			require.NoError(t, db.Table("autonomy_effects").Count(&count).Error)
			require.Equal(t, int64(2), count, "duplicate checks produce one audit/outbox transition")
			visible, err := m.projectAutonomyFactOn(ctx, db, 1, 99, true, 2)
			require.NoError(t, err)
			require.True(t, visible)
			failed = false
			if condition == "P3" {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("budget_state", "NORMAL").Error)
			}
			for range 2 {
				require.NoError(t, m.watchAutonomy(ctx, 1, 99))
			}
			p, err = q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.Equal(t, 3, p.Version)
			require.JSONEq(t, original, p.Holds, "recovery preserves every unrelated hold field")
			require.NoError(t, db.Table("autonomy_effects").Count(&count).Error)
			require.Equal(t, int64(4), count)
			visible, err = m.projectAutonomyFactOn(ctx, db, 1, 99, true, 2)
			require.NoError(t, err)
			require.False(t, visible)
			visible, err = m.projectAutonomyFactOn(ctx, db, 1, 99, false, 3)
			require.NoError(t, err)
			require.True(t, visible)
		})
	}
}

func TestAutonomyWatchFailureRollsBackHoldVersionAndAudit(t *testing.T) {
	for _, boundary := range []string{"principals", "preconditions", "audit", "outbox"} {
		t.Run(boundary, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "budget_state": "EXHAUSTED"}).Error)
			m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return nil }
			var before int64
			require.NoError(t, db.Table("provision_probe").Count(&before).Error)
			failure := errors.New("autonomy source failed")
			switch boundary {
			case "principals":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "preconditions":
				m.modeGovernance.PreconditionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
					return false, nil, failure
				}
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			}
			require.ErrorIs(t, m.watchAutonomy(ctx, 1, 99), failure)
			p, err := query.BindProjects(db, m.service.names).WithContext(ctx).Where(query.BindProjects(db, m.service.names).ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.Equal(t, 1, p.Version)
			require.Nil(t, p.AutonomyVersion, "a failed transition cannot leave an episode marker")
			require.JSONEq(t, "[]", p.Holds)
			var count int64
			require.NoError(t, db.Table("provision_probe").Count(&count).Error)
			require.Equal(t, before, count, "audit/outbox failure rolls back all new effects")
		})
	}
}

func TestAutonomyWatchPersistsExactCurrentEpisodeAcrossABA(t *testing.T) {
	for _, finalLost := range []bool{true, false} {
		t.Run(map[bool]string{true: "lost_restored_lost", false: "restored_lost_restored"}[finalLost], func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("team_mode", "AUTONOMOUS").Error)
			m.assignmentGuard = func(context.Context, *gorm.DB, string) error { return nil }
			var transitions []events.Event
			m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
				transitions = append(transitions, event)
				return guid.ID(1000 + len(transitions)), nil
			})
			states := []string{"EXHAUSTED", "NORMAL", "EXHAUSTED"}
			if !finalLost {
				states = append(states, "NORMAL")
			}
			for _, state := range states {
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("budget_state", state).Error)
				require.NoError(t, m.watchAutonomy(ctx, 1, 99))
			}
			require.Len(t, transitions, len(states))
			current := transitions[len(transitions)-1]
			old := transitions[len(transitions)-3]
			require.Equal(t, old.Type, current.Type)
			require.NotEqual(t, old.SubjectVersion, current.SubjectVersion)
			payload, ok := current.Data.(map[string]any)
			require.True(t, ok)
			require.Equal(t, current.SubjectVersion, payload["autonomy_version"])
			visible, err := m.projectAutonomyFactOn(ctx, db, 1, 99, finalLost, old.SubjectVersion)
			require.NoError(t, err)
			require.False(t, visible, "an earlier same-state episode stays obsolete")
			visible, err = m.projectAutonomyFactOn(ctx, db, 1, 99, finalLost, current.SubjectVersion)
			require.NoError(t, err)
			require.True(t, visible)
			var marker struct{ AutonomyVersion *int64 }
			require.NoError(t, db.Table(m.service.names.Projects.String()).Select("autonomy_version").Where("project_id=99").Scan(&marker).Error)
			require.NotNil(t, marker.AutonomyVersion)
			require.Equal(t, current.SubjectVersion, *marker.AutonomyVersion, "the source records the exact current autonomy transition, including restoration")
			// General project edits must not replace the autonomy episode identity.
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("version", gorm.Expr("version + 1")).Error)
			require.NoError(t, m.watchAutonomy(ctx, 1, 99))
			require.Len(t, transitions, len(states), "an unchanged restriction creates no new episode")
			require.NoError(t, db.Table(m.service.names.Projects.String()).Select("autonomy_version").Where("project_id=99").Scan(&marker).Error)
			require.Equal(t, current.SubjectVersion, *marker.AutonomyVersion)
			visible, err = m.projectAutonomyFactOn(ctx, db, 1, 99, finalLost, current.SubjectVersion)
			require.NoError(t, err)
			require.True(t, visible, "unrelated project versions do not retire the episode")
		})
	}
}

func TestAutonomyEpisodeIsClearedByModeMutation(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		m, db, ctx := projectModeHarness(t)
		require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "autonomy_version": 1}).Error)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "HYBRID", Reason: "Human reviews current project authority", ReauthProof: "proof"}, "cookie", takeover, []guid.ID{11})
			return err
		}))
		var marker struct{ AutonomyVersion *int64 }
		require.NoError(t, db.Table(m.service.names.Projects.String()).Select("autonomy_version").Where("project_id=99").Scan(&marker).Error)
		require.Nil(t, marker.AutonomyVersion, "explicit mode changes and takeovers retire autonomy episodes")
	}
}

type autonomyQueueProbe struct {
	jobs.Queue
	requests []jobs.EnqueueRequest
}

func (p *autonomyQueueProbe) Enqueue(_ context.Context, _ *gorm.DB, in jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	p.requests = append(p.requests, in)
	return jobs.EnqueueResult{JobID: 1, Created: true}, nil
}

func TestAutonomyWatchEventsQueueCurrentEvaluationAndHourlyFallback(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	queue := &autonomyQueueProbe{}
	m.autonomyQueue = queue
	for _, project := range []guid.ID{99, 0} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.autonomyWatchEventOn(ctx, tx, events.Envelope{EventID: 500 + project, TenantID: 1, ProjectID: project})
		}))
	}
	require.Len(t, queue.requests, 2)
	for _, req := range queue.requests {
		require.Equal(t, autonomyWatchJob, req.Kind)
		require.Equal(t, "delivery:autonomy:1", req.ConcurrencyKey)
		require.Zero(t, req.Delay, "loss events must not wait for the hourly fallback")
	}
	var schedule jobs.Schedule
	for _, s := range m.JobSchedules() {
		if s.Kind == autonomyWatchJob {
			schedule = s
		}
	}
	require.Equal(t, time.Hour, schedule.Interval)
	require.Equal(t, jobs.ScopePerTenant, schedule.TenantScope)
	require.Error(t, m.autonomyWatchHandler().Run(ctx, jobs.Job{}))
	require.Error(t, m.autonomyWatchHandler().Run(ctx, jobs.Job{TenantID: 1, Payload: []byte("bad")}))
	for _, scenario := range []string{"HYBRID", "CLOSED", "ARCHIVED"} {
		require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"team_mode": "AUTONOMOUS", "stage": "ACTIVE", "archived_at": nil}).Error)
		if scenario == "HYBRID" {
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("team_mode", scenario).Error)
		} else if scenario == "CLOSED" {
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("stage", scenario).Error)
		} else {
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Updates(map[string]any{"stage": "CLOSED", "archived_at": time.Now()}).Error)
		}
		m.assignmentGuard = func(context.Context, *gorm.DB, string) error {
			t.Fatal("ineligible project must not be evaluated")
			return nil
		}
		require.NoError(t, m.watchAutonomy(ctx, 1, 0), scenario)
	}
}
