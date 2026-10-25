package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/planning/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGoalRefreshScheduleUsesSystemSourcesAndRollsBackFailures(t *testing.T) {
	h := generationHarness(t)
	member := goalTree(t, h)
	h.m.now = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	var handler jobs.Handler
	for _, hdl := range h.m.JobHandlers() {
		if hdl.Kind == "planning.goal_refresh" {
			handler = hdl
		}
	}
	require.NotNil(t, handler.Run)
	for _, schedule := range h.m.JobSchedules() {
		if schedule.Kind == handler.Kind {
			require.Equal(t, 15*time.Minute, schedule.Interval)
			require.Equal(t, jobs.ScopePerTenant, schedule.TenantScope)
		}
	}
	require.ErrorIs(t, handler.Run(claims(t, 2), jobs.Job{TenantID: 1}), ErrForbidden)
	read := func() model.Goal {
		var row model.Goal
		require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("tenant_id=? AND id=?", 1, member.ID).First(&row).Error)
		return row
	}
	failure := errors.New("audit unavailable")
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.refreshGoalOn(t.Context(), tx, 1, member.ID) }), failure)
	require.Equal(t, member.Version, read().Version)
	h.m.audit = appender
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 1}))
	row := read()
	require.Equal(t, "AT_RISK", row.State)
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Equal(t, row.Version, read().Version, "unchanged source replay must not rewrite or republish")
	h.m.now = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 1}))
	require.Equal(t, "ACTIVE", read().State)
}

func TestScheduledAutomaticGoalDeduplicatesAcceptedWorkAndPreservesMissingSources(t *testing.T) {
	h := generationHarness(t)
	member := goalTree(t, h)
	var g PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &g))
	g.KeyResults[0].Source, g.KeyResults[0].Metric, g.KeyResults[0].Current = "AUTO", "ACCEPTED_WORK_ITEMS", 0
	r := activateGoal(t, h, create(t, h, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &g}))
	late := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	h.facts.Work = append(h.facts.Work, h.facts.Work[0], delivery.PlanningWorkFact{ID: 999, State: "ACCEPTED", AccountablePrincipalID: 2, AcceptedAt: &late})
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.refreshGoalOn(t.Context(), tx, 1, r.ID) }))
	var row model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", r.ID).First(&row).Error)
	require.NoError(t, json.Unmarshal([]byte(row.Content), &g))
	require.EqualValues(t, 1, g.KeyResults[0].Current, "cost and client-acceptance availability cannot erase actual accepted-work facts")
	reader := h.m.scheduledFacts.ReadOn
	h.m.scheduledFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{}, ErrUnavailable
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.refreshGoalOn(t.Context(), tx, 1, r.ID) }), ErrUnavailable)
	h.m.scheduledFacts.ReadOn = reader
	var unchanged model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", r.ID).First(&unchanged).Error)
	require.Equal(t, row.ContentHash, unchanged.ContentHash)
	require.Equal(t, row.Version, unchanged.Version)
}

func TestGoalRefreshFailureDoesNotAdvanceCurrentGoal(t *testing.T) {
	for _, mode := range []string{"read", "content", "period", "metric", "missing_facts", "team_missing", "team_failure", "write", "inactive"} {
		t.Run(mode, func(t *testing.T) {
			h := generationHarness(t)
			r := goalTree(t, h)
			var body PlanningGoalInput
			require.NoError(t, json.Unmarshal(r.Content, &body))
			body.KeyResults[0].Source = "AUTO"
			body.KeyResults[0].Metric = "ACCEPTED_WORK_ITEMS"
			updates := map[string]any{}
			switch mode {
			case "read":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("refresh_goal_read", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Goals.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "content":
				updates["content"] = "["
			case "period":
				updates["period_start"] = "bad"
			case "metric":
				body.KeyResults[0].Metric = "UNOBSERVED_METRIC"
			case "missing_facts":
				h.m.scheduledFacts.ReadOn = nil
			case "team_missing":
				updates["project_id"] = 0
				h.m.scheduledFacts.ProjectsOn = nil
			case "team_failure":
				updates["project_id"] = 0
				h.m.scheduledFacts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]guid.ID, error) {
					return nil, ErrUnavailable
				}
			case "write":
			case "inactive":
				updates["state"] = "DRAFT"
			}
			if mode != "content" {
				data, e := json.Marshal(body)
				require.NoError(t, e)
				updates["content"] = string(data)
			}
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", r.ID).Updates(updates).Error)
			if mode == "write" {
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("refresh_goal_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Goals.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.refreshGoalOn(t.Context(), tx, 1, r.ID) })
			require.NoError(t, h.db.Callback().Query().Remove("refresh_goal_read"))
			require.NoError(t, h.db.Callback().Update().Remove("refresh_goal_write"))
			if mode == "inactive" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var row model.Goal
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", r.ID).First(&row).Error)
			require.Equal(t, r.Version, row.Version)
		})
	}
	h := generationHarness(t)
	require.ErrorIs(t, h.m.refreshGoals(t.Context(), 0), ErrInvalid)
	r := goalTree(t, h)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.refreshGoalOn(claims(t, 2), tx, 1, r.ID) }), ErrForbidden)
}
