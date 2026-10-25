package planning

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlanBaselineUsesScheduledWeightsAndRealCompletion(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	var report model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&report).Error)
	start, end, err := reportPeriod(&report)
	require.NoError(t, err)
	now := start.Add(3 * 24 * time.Hour)
	h.m.now = func() time.Time { return now }
	due := start.Add(24 * time.Hour)
	future := end
	completed := start.Add(time.Hour)
	item := linkedItem(g)
	workID := guid.ID(100)
	item.WorkItemID, item.Category = &workID, ""
	item.DueAt, item.Weight = &due, 3
	free := PlanningPlanItem{Title: "Study", GoalID: g.ID, KR: "accepted", Category: "LEARNING", Weight: 1, DueAt: &future, CompletedAt: &completed, CompletionEvidence: "Reviewed training notes"}
	body := PlanningPlanInput{Items: []PlanningPlanItem{item, free}}
	encoded, hash, err := hashJSON(body)
	require.NoError(t, err)
	plan := model.Plan{TenantID: 1, ID: 999, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: r.PeriodStart, State: "CONFIRMED", Content: encoded, ContentHash: hash, Version: 1}
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&plan).Error)
	read := func() PlanningReportProgress {
		t.Helper()
		var body PlanningReportInput
		require.NoError(t, json.Unmarshal(r.Content, &body))
		out := PlanningCheck{Result: "PASS"}
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.checkProgressOn(claims(t, 2), tx, 1, &report, body, &out) }))
		return out.Progress
	}
	got := read()
	require.Equal(t, "PLAN_SCHEDULE", got.ExpectedSource)
	require.InDelta(t, .75, *got.Expected, .00001)
	require.InDelta(t, 1, *got.Computed, .00001)
	h.facts.Work[0].AcceptedAt = &now
	require.InDelta(t, 1, *read().Computed, .00001, "acceptance at the observation instant is an actual completion")
	h.facts.Work[0].AcceptedAt = nil
	require.InDelta(t, .25, *read().Computed, .00001, "plan claims cannot replace accepted-work evidence")
	h.facts.Work[0].AcceptedAt = &future
	require.InDelta(t, .25, *read().Computed, .00001, "late acceptance cannot complete earlier baseline")
	body.Items[1].CompletionEvidence = ""
	encoded, _, err = hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", plan.ID).Update("content", encoded).Error)
	require.InDelta(t, 0, *read().Computed, .00001, "free-task completion requires evidence")
	previous := h.m.facts.ReadOn
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{}, ErrUnavailable
	}
	var out PlanningCheck
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.checkProgressOn(claims(t, 2), tx, 1, &report, PlanningReportInput{}, &out)
	}), ErrUnavailable)
	h.m.facts.ReadOn = previous
}

func TestPlanScheduleRejectsOutsidePeriodAndSpoofedCompletion(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	start, end, err := h.m.periodForOn(t.Context(), h.db, 1, "WEEK", "2026-10-19")
	require.NoError(t, err)
	before := start.Add(-time.Millisecond)
	after := end.Add(time.Millisecond)
	for _, due := range []time.Time{before, after} {
		require.ErrorIs(t, validatePlanSchedule([]PlanningPlanItem{{DueAt: &due}}, start, end), ErrInvalid)
	}
	item := linkedItem(g)
	workID := guid.ID(100)
	item.WorkItemID, item.Category = &workID, ""
	completed := h.m.now().Add(-time.Hour)
	item.CompletedAt, item.CompletionEvidence = &completed, "untrusted assertion"
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateItems(claims(t, 2), tx, 1, 3, 2, []PlanningPlanItem{item}) }), ErrInvalid)
}

func TestAggregateProgressUsesCanonicalTargetWeightsAndLiveChildren(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	first := reportDraft(t, h)
	var goal model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).First(&goal).Error)
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal([]byte(goal.Content), &body))
	body.Weight, body.KeyResults[0].Current, body.KeyResults[0].Evidence = 3, 10, "accepted human evidence"
	encoded, _, err := hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", encoded).Error)
	goal.ID, goal.PrincipalID = 88881, 4
	body.Weight, body.KeyResults[0].Current = 1, 0
	goal.Content, goal.ContentHash, err = hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Create(&goal).Error)
	var second PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = h.m.createOn(claims(t, 4), tx, 1, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 4, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		return err
	}))
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id IN ?", []guid.ID{first.ID, second.ID}).Update("state", "SUBMITTED").Error)
	read := func() PlanningReportProgress {
		t.Helper()
		var progress PlanningReportProgress
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			aggregate := model.Report{TenantID: 1, ID: 999999, ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Content: `{"scope":"PROJECT","calendar_timezone":"Asia/Shanghai"}`}
			out := PlanningCheck{Result: "PASS"}
			err := h.m.checkProgressOn(claims(t, 3), tx, 1, &aggregate, PlanningReportInput{CalendarTimezone: "Asia/Shanghai"}, &out)
			progress = out.Progress
			return err
		}))
		return progress
	}
	got := read()
	require.NotNil(t, got.Computed)
	require.InDelta(t, .75, *got.Computed, .00001, "three target-weight units complete, one incomplete")
	body.Weight, body.KeyResults[0].Current, body.KeyResults[0].Evidence = 1, 10, "later actual progress"
	encoded, _, err = hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("content", encoded).Error)
	require.InDelta(t, 1, *read().Computed, .00001, "aggregate rereads live canonical child facts")
	body.Weight = 0
	encoded, _, err = hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("content", encoded).Error)
	missing := read()
	require.Nil(t, missing.Computed, "one absent canonical child weight invalidates the aggregate rather than guessing")
	require.Contains(t, missing.MissingSources, "CHILD_PROGRESS_WEIGHTS")
}

func TestGlobalMemberBaselineCombinesProjectSchedulesWithoutLosingWeights(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	draft := reportDraft(t, h)
	var report model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", draft.ID).First(&report).Error)
	report.ProjectID = 0
	start, end, err := reportPeriod(&report)
	require.NoError(t, err)
	now := start.Add(3 * 24 * time.Hour)
	h.m.now = func() time.Time { return now }
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	due, later, done := start.Add(time.Hour), end, now
	for i, project := range []guid.ID{3, 4} {
		item := linkedItem(g)
		item.Weight = float64(1 + 2*i)
		item.DueAt = &later
		if i == 0 {
			item.DueAt, item.CompletedAt, item.CompletionEvidence = &due, &done, "observed learning receipt"
		}
		body, hash, err := hashJSON(PlanningPlanInput{Items: []PlanningPlanItem{item}})
		require.NoError(t, err)
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&model.Plan{TenantID: 1, ID: guid.ID(88000 + i), ProjectID: project, PrincipalID: 2, PeriodKind: report.PeriodKind, PeriodStart: report.PeriodStart, State: "CONFIRMED", Content: body, ContentHash: hash, Version: 1}).Error)
	}
	var out PlanningCheck
	require.NoError(t, h.m.checkProgressOn(claims(t, 2), h.db, 1, &report, PlanningReportInput{}, &out))
	require.InDelta(t, .25, *out.Progress.Computed, .00001)
	require.InDelta(t, .25, *out.Progress.Expected, .00001)
	require.Equal(t, "PLAN_SCHEDULE", out.Progress.ExpectedSource)
	validate := func(items []PlanningPlanItem) error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateReportItems(claims(t, 2), tx, 1, &report, items) })
	}
	require.NoError(t, validate([]PlanningPlanItem{linkedItem(g)}))
	require.ErrorIs(t, validate(nil), ErrInvalid)
	wrong := linkedItem(g)
	wrong.GoalID = 991991
	require.Error(t, validate([]PlanningPlanItem{wrong}))
	wrong = linkedItem(g)
	wrong.KR = "unknown"
	require.ErrorIs(t, validate([]PlanningPlanItem{wrong}), ErrInvalid)
}

func TestPlanBaselineRejectsCorruptAndIncompleteCanonicalSources(t *testing.T) {
	h := setup(t)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	report := model.Report{ProjectID: 3, PrincipalID: 2}
	var out PlanningReportProgress
	available, err := h.m.planReportProgressOn(claims(t, 2), h.db, 1, &report, &model.Plan{Content: "{"}, start, end, &out)
	require.Error(t, err)
	require.False(t, available)
	for _, item := range []PlanningPlanItem{{}, {DueAt: &start, Weight: -1}} {
		encoded, err := json.Marshal(PlanningPlanInput{Items: []PlanningPlanItem{item}})
		require.NoError(t, err)
		available, err = h.m.planReportProgressOn(claims(t, 2), h.db, 1, &report, &model.Plan{Content: string(encoded)}, start, end, &out)
		require.False(t, available)
		if item.DueAt == nil {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrInvalid)
		}
	}
	encoded, _, err := hashJSON(PlanningPlanInput{})
	require.NoError(t, err)
	available, err = h.m.planReportProgressOn(claims(t, 2), h.db, 1, &report, &model.Plan{Content: encoded}, start, end, &out)
	require.NoError(t, err)
	require.False(t, available)
	parent := model.Report{ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}
	value, err := h.m.childReportProgressOn(claims(t, 2), h.db, 1, &parent, []*model.Report{{ProjectID: 3, PeriodKind: "DAY", PeriodStart: "2026-10-19"}, {ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"}})
	require.NoError(t, err)
	require.Nil(t, value, "temporal provenance and project peers cannot be counted as member targets")
}

func TestProjectProgressResolvesGlobalMemberProvenanceWithProjectTargetWeights(t *testing.T) {
	h := generationHarness(t)
	goal := goalTree(t, h)
	var target model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).First(&target).Error)
	var input PlanningGoalInput
	require.NoError(t, json.Unmarshal([]byte(target.Content), &input))
	input.Weight = 2
	input.KeyResults[0].Current = 10
	input.KeyResults[0].Evidence = "accepted target evidence"
	encoded, _, err := hashJSON(input)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("content", encoded).Error)
	require.NoError(t, h.m.generateReports(t.Context(), 1))
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("project_id=0 AND principal_id=2 AND period_kind=?", "WEEK").Update("state", "SUBMITTED").Error)
	var parent model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("project_id=3 AND principal_id=0 AND period_kind=?", "WEEK").First(&parent).Error)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal([]byte(parent.Content), &body))
	require.Len(t, body.Children, 1, "project references its contributing tenant-wide member report")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		children, err := h.m.childReportsOn(claims(t, 3), tx, 1, &parent)
		require.NoError(t, err)
		require.Len(t, children, 1)
		require.Zero(t, children[0].ProjectID, "no duplicate per-project member identity")
		out := PlanningCheck{Result: "PASS"}
		require.NoError(t, h.m.checkProgressOn(claims(t, 3), tx, 1, &parent, body, &out))
		require.NotNil(t, out.Progress.Computed)
		require.InDelta(t, 1, *out.Progress.Computed, .00001, "project-specific goals weight the tenant-wide child's live progress")
		return nil
	}))
}

func TestChildTargetWeightsRejectCorruptSourcesAndIgnoreUnrelatedTargets(t *testing.T) {
	for _, name := range []string{"report_period", "report_content", "goal_content", "goal_period", "wrong_level", "outside_period"} {
		t.Run(name, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			child := model.Report{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Content: `{"calendar_timezone":"Asia/Shanghai"}`}
			var goal model.Goal
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).First(&goal).Error)
			var body PlanningGoalInput
			require.NoError(t, json.Unmarshal([]byte(goal.Content), &body))
			body.Weight = 2
			updates := map[string]any{}
			switch name {
			case "report_period":
				child.PeriodKind = "UNKNOWN"
			case "report_content":
				child.Content = "{"
			case "goal_content":
				updates["content"] = "{"
			case "goal_period":
				updates["period_kind"] = "UNKNOWN"
			case "wrong_level":
				body.Level = "PROJECT"
			case "outside_period":
				updates["period_start"] = "2026-11-01"
			}
			if _, corrupt := updates["content"]; !corrupt {
				encoded, _, err := hashJSON(body)
				require.NoError(t, err)
				updates["content"] = encoded
			}
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Updates(updates).Error)
			var weight float64
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				weight, err = h.m.childTargetWeightOn(claims(t, 2), tx, 1, &child)
				return err
			})
			if name == "wrong_level" || name == "outside_period" {
				require.NoError(t, err)
				require.Zero(t, weight, "unrelated goals must not supply an aggregate target weight")
			} else {
				require.Error(t, err, "corrupt canonical sources must fail rather than produce zero progress")
			}
		})
	}
}

func TestPlanBaselineRejectsOutOfPeriodScheduleBeforeReadingFacts(t *testing.T) {
	h := setup(t)
	start := h.m.now()
	end := start.AddDate(0, 0, 7)
	due := end.Add(time.Nanosecond)
	encoded, _, err := hashJSON(PlanningPlanInput{Items: []PlanningPlanItem{{DueAt: &due, Weight: 1}}})
	require.NoError(t, err)
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		t.Fatal("invalid schedule must be rejected before fact access")
		return delivery.PlanningSnapshot{}, nil
	}
	var out PlanningReportProgress
	available, err := h.m.planReportProgressOn(claims(t, 2), h.db, 1, &model.Report{ProjectID: 3, PrincipalID: 2}, &model.Plan{Content: encoded}, start, end, &out)
	require.ErrorIs(t, err, ErrInvalid)
	require.False(t, available)
	require.Nil(t, out.Computed)
}

func TestAggregateProgressPropagatesInvalidChildAndMissingProgress(t *testing.T) {
	for _, source := range []string{"invalid_period", "missing_progress", "fact_failure"} {
		t.Run(source, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			var goal model.Goal
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).First(&goal).Error)
			var body PlanningGoalInput
			require.NoError(t, json.Unmarshal([]byte(goal.Content), &body))
			body.Weight = 1
			if source == "missing_progress" {
				body.KeyResults[0].Source, body.KeyResults[0].Metric = "AUTO", "UNAVAILABLE_METRIC"
			}
			if source == "fact_failure" {
				body.KeyResults[0].Source, body.KeyResults[0].Metric = "AUTO", "ACCEPTED_WORK_ITEMS"
			}
			encoded, _, err := hashJSON(body)
			require.NoError(t, err)
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", encoded).Error)
			child := model.Report{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Content: `{"calendar_timezone":"Asia/Shanghai"}`}
			parent := model.Report{ProjectID: 3, PeriodKind: child.PeriodKind, PeriodStart: child.PeriodStart}
			if source == "invalid_period" {
				child.PeriodStart, parent.PeriodStart = "bad-date", "bad-date"
			}
			if source == "fact_failure" {
				h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
					return delivery.PlanningSnapshot{}, ErrUnavailable
				}
			}
			var progress *float64
			err = h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				progress, err = h.m.childReportProgressOn(claims(t, 2), tx, 1, &parent, []*model.Report{&child})
				return err
			})
			if source == "missing_progress" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Nil(t, progress, "unavailable child facts must never become zero progress")
		})
	}
}

func TestChildTargetWeightsFailClosedWhenTargetsCannotBeEnumerated(t *testing.T) {
	for _, source := range []string{"too_many", "storage_failure"} {
		t.Run(source, func(t *testing.T) {
			h := setup(t)
			if source == "too_many" {
				rows := make([]model.Goal, 501)
				for i := range rows {
					rows[i] = model.Goal{TenantID: 1, ID: guid.ID(900000 + i), ProjectID: 3, PrincipalID: 2, State: "ACTIVE", PeriodKind: "MONTH", PeriodStart: "2026-10-01", Content: `{}`}
				}
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).CreateInBatches(rows, 100).Error)
			} else {
				require.NoError(t, h.db.Migrator().DropTable(h.m.names.Goals.String()))
			}
			child := model.Report{ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: "2026-10-19", Content: `{}`}
			weight, err := h.m.childTargetWeightOn(claims(t, 2), h.db, 1, &child)
			require.Error(t, err)
			require.Zero(t, weight, "a partial or failed target enumeration must not determine aggregate weights")
			if source == "too_many" {
				require.ErrorIs(t, err, ErrUnavailable)
			}
		})
	}
}
