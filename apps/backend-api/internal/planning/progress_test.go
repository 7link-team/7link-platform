package planning

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/planning/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestElapsedWorkdaysUsesLocalCalendarAndBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 7) // Includes the 25-hour DST Sunday.
	for _, tc := range []struct {
		at   time.Time
		want float64
	}{
		{start.Add(-time.Hour), 0},
		{start.AddDate(0, 0, 3), .6},
		{time.Date(2026, 10, 30, 12, 0, 0, 0, loc), .9},
		{end.Add(time.Hour), 1},
	} {
		got := elapsedWorkdays(start, end, tc.at, loc, []int{1, 2, 3, 4, 5})
		require.NotNil(t, got)
		require.InDelta(t, tc.want, *got, .00001)
	}
	sunday := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	got := elapsedWorkdays(sunday, sunday.AddDate(0, 0, 1), sunday.Add(12*time.Hour+30*time.Minute), loc, []int{7})
	require.NotNil(t, got)
	require.InDelta(t, .5, *got, .00001)
	require.Nil(t, elapsedWorkdays(start, end, start, loc, nil))
	require.Nil(t, elapsedWorkdays(end, start, start, loc, []int{1}))
}

func TestReportB6UsesCurrentGoalFactsAndExplanationAtThreshold(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	// Thursday midnight: three of five local working days have elapsed.
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	h.m.now = func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, loc) }
	var goal PlanningGoalInput
	require.NoError(t, json.Unmarshal(g.Content, &goal))
	goal.KeyResults[0].Current = 3
	goal.KeyResults[0].Evidence = "human evidence"
	encoded, _, err := hashJSON(goal)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", encoded).Error)
	body := PlanningReportInput{Uncompleted: "Work remains", Retrospective: "Review actual evidence", NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "GREEN"}
	var original PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &original))
	body.BoundaryStart, body.BoundaryEnd = original.BoundaryStart, original.BoundaryEnd
	check := func() PlanningCheck {
		t.Helper()
		var result PlanningCheck
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			row, _, err := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
			if err != nil {
				return err
			}
			row.Content, _, err = hashJSON(body)
			if err != nil {
				return err
			}
			result, err = h.m.checkReport(claims(t, 2), tx, 1, row)
			return err
		}))
		return result
	}
	got := check()
	require.NotNil(t, got.Progress.Computed)
	require.NotNil(t, got.Progress.Expected)
	require.InDelta(t, .3, *got.Progress.Computed, .00001)
	require.InDelta(t, .6, *got.Progress.Expected, .00001)
	require.Contains(t, got.Issues, PlanningIssue{Code: "B6_WATERMELON", Blocking: true})
	body.StatusExplanation = strings.Repeat("a", 19)
	require.Contains(t, check().Issues, PlanningIssue{Code: "B6_WATERMELON", Blocking: true})
	body.StatusExplanation = strings.Repeat("a", 20)
	require.Contains(t, check().Issues, PlanningIssue{Code: "N5_STATUS_EXPLAINED"})
	body.StatusExplanation = ""
	body.DeclaredConfidence = "YELLOW"
	require.NotContains(t, check().Issues, PlanningIssue{Code: "B6_WATERMELON", Blocking: true})
	body.DeclaredConfidence = "GREEN"
	var edited PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		edited, err = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return err
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.decideReportOn(claims(t, 2), tx, 1, r.ID, PlanningDecisionInput{Action: "SUBMIT", Version: edited.Version, ExpectedContentHash: edited.ContentHash})
		return err
	}), ErrConflict)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		stored, err := h.m.readOn(claims(t, 2), tx, 1, r.ID, "report")
		if err != nil {
			return err
		}
		require.Equal(t, "DRAFT", stored.State, "blocked submission must not change state")
		require.Equal(t, edited.Version, stored.Version)
		return nil
	}))
	// A real confirmed plan with unavailable schedule is disclosed, not
	// replaced by elapsed time or declared completion claims.
	plan := model.Plan{TenantID: 1, ID: 999, ProjectID: 3, PrincipalID: 2, PeriodKind: "WEEK", PeriodStart: r.PeriodStart, State: "CONFIRMED", Content: `{"items":[]}`, ContentHash: "plan", Version: 1}
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&plan).Error)
	got = check()
	require.Nil(t, got.Progress.Expected)
	require.Nil(t, got.Progress.Computed)
	require.Contains(t, got.Progress.MissingSources, "PLAN_PROGRESS_BASELINE")
}

func TestReportProgressUsesLiveAutoFactsAndFrozenCalendar(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &body))
	var goal PlanningGoalInput
	require.NoError(t, json.Unmarshal(g.Content, &goal))
	goal.KeyResults[0].Source = "AUTO"
	goal.KeyResults[0].Metric = "ACCEPTED_WORK_ITEMS"
	goal.KeyResults[0].Current = 9 // Cached values are deliberately stale.
	encoded, _, err := hashJSON(goal)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", encoded).Error)
	read := func() PlanningCheck {
		t.Helper()
		var got PlanningCheck
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			row, _, err := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
			if err != nil {
				return err
			}
			got, err = h.m.checkReport(claims(t, 2), tx, 1, row)
			return err
		}))
		return got
	}
	got := read()
	require.NotNil(t, got.Progress.Computed)
	require.InDelta(t, .1, *got.Progress.Computed, .00001)
	expected := *got.Progress.Expected
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{Timezone: settingPtr("America/Los_Angeles")})
		return err
	}))
	require.InDelta(t, expected, *read().Progress.Expected, .00001, "stored calendar survives team timezone changes")
	late := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	h.facts.Work[0].AcceptedAt = &late
	got = read()
	require.InDelta(t, 0, *got.Progress.Computed, .00001, "acceptance after report period never counts")
	h.facts.Work[0].AcceptedAt = nil
	got = read()
	require.InDelta(t, 0, *got.Progress.Computed, .00001)
	goal.KeyResults[0].Metric = "MISSING_METRIC"
	encoded, _, err = hashJSON(goal)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", encoded).Error)
	got = read()
	require.Nil(t, got.Progress.Computed)
	require.Contains(t, got.Progress.MissingSources, "GOAL_PROGRESS")
	// Missing workdays are an unavailable expected value, never fabricated zero.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 2, PlanningSettingsPatch{Workdays: settingPtr([]int{})})
		return err
	}))
	require.Contains(t, read().Progress.MissingSources, "WORKDAY_CALENDAR")
	body.StatusExplanation = strings.Repeat("x", 2001)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, body)
		return err
	}), ErrInvalid)
}

func TestProgressThresholdSettingsAndWeightedGoalFacts(t *testing.T) {
	for _, invalid := range []float64{-.1, .0999, .6001, math.NaN(), math.Inf(1)} {
		v := defaultPlanningSettings().PlanningSettingsValue
		v.WatermelonLagThreshold = invalid
		require.False(t, validPlanningSettings(v))
	}
	h := setup(t)
	var settings PlanningSettings
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		settings, err = h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{WatermelonLagThreshold: settingPtr(.4)})
		return err
	}))
	require.InDelta(t, .4, settings.WatermelonLagThreshold, .00001)
	settings, err := h.m.readSettingsOn(t.Context(), h.db, 1)
	require.NoError(t, err)
	require.InDelta(t, .4, settings.WatermelonLagThreshold, .00001)
	require.InDelta(t, .75, goalProgress(PlanningGoalInput{KeyResults: []PlanningKeyResult{{Target: 10, Current: 10, Weight: 3}, {Target: 10, Current: 0, Weight: 1}}}), .00001)
	require.InDelta(t, .5, goalProgress(PlanningGoalInput{KeyResults: []PlanningKeyResult{{Target: 10, Current: 10}, {Target: 10, Current: 0}}}), .00001)
}

func TestProgressSourceOutagesCannotBecomeZeroProgress(t *testing.T) {
	for _, source := range []string{"Settings", "Plans", "Goals"} {
		t.Run(source, func(t *testing.T) {
			h := setup(t)
			goalTree(t, h)
			r := reportDraft(t, h)
			var body PlanningReportInput
			require.NoError(t, json.Unmarshal(r.Content, &body))
			name := h.m.names.Settings.String()
			if source == "Plans" {
				name = h.m.names.Plans.String()
			}
			if source == "Goals" {
				name = h.m.names.Goals.String()
			}
			require.NoError(t, h.db.Migrator().DropTable(name))
			err := h.db.Transaction(func(tx *gorm.DB) error {
				out := PlanningCheck{Result: "PASS"}
				row := model.Report{TenantID: 1, ID: r.ID, ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, PeriodKind: r.PeriodKind, PeriodStart: r.PeriodStart, Content: string(r.Content)}
				err := h.m.checkProgressOn(claims(t, 2), tx, 1, &row, body, &out)
				require.Nil(t, out.Progress.Computed, "unavailable facts must never be reported as zero")
				return err
			})
			require.Error(t, err)
		})
	}
}

func TestAggregateProgressDoesNotGuessChildWeights(t *testing.T) {
	h := setup(t)
	goalTree(t, h)
	child := reportDraft(t, h)
	// Persist a submitted child fixture; drafts are not progress baselines.
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", child.ID).Update("state", "SUBMITTED").Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		aggregate, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		if err != nil {
			return err
		}
		row, _, err := h.m.reportOn(claims(t, 3), tx, 1, aggregate.ID, "report_read", false)
		if err != nil {
			return err
		}
		var body PlanningReportInput
		require.NoError(t, json.Unmarshal(aggregate.Content, &body))
		out := PlanningCheck{Result: "PASS"}
		require.NoError(t, h.m.checkProgressOn(claims(t, 3), tx, 1, row, body, &out))
		require.Nil(t, out.Progress.Computed, "missing child target weights must not become an unweighted average")
		require.Nil(t, out.Progress.Lag)
		require.NotNil(t, out.Progress.Expected)
		require.Contains(t, out.Progress.MissingSources, "CHILD_PROGRESS_WEIGHTS")
		return nil
	}))
}

func TestProjectProgressFallsBackToOwnGoalsWithoutChildren(t *testing.T) {
	h := setup(t)
	goalTree(t, h)
	reportDraft(t, h) // An unsubmitted child must not suppress the goal fallback.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		aggregate, err := h.m.createAggregationOn(claims(t, 3), tx, 1, PlanningAggregationRequest{Scope: "PROJECT", ProjectID: 3, PeriodKind: "WEEK", PeriodStart: "2026-10-19"})
		if err != nil {
			return err
		}
		row, _, err := h.m.reportOn(claims(t, 3), tx, 1, aggregate.ID, "report_read", false)
		if err != nil {
			return err
		}
		var body PlanningReportInput
		require.NoError(t, json.Unmarshal(aggregate.Content, &body))
		out := PlanningCheck{Result: "PASS"}
		require.NoError(t, h.m.checkProgressOn(claims(t, 3), tx, 1, row, body, &out))
		require.NotNil(t, out.Progress.Computed, "a real project goal supplies the no-child fallback")
		require.NotNil(t, out.Progress.Expected)
		require.NotNil(t, out.Progress.Lag)
		require.Empty(t, out.Progress.MissingSources)
		return nil
	}))
}

func TestSameMonthProgressKeepsReportCalendarAfterTimezoneChange(t *testing.T) {
	h := setup(t)
	goalTree(t, h)
	r := create(t, h, "report", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01"})
	read := func() PlanningReportProgress {
		t.Helper()
		var out PlanningCheck
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			row, _, err := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
			if err != nil {
				return err
			}
			var body PlanningReportInput
			require.NoError(t, json.Unmarshal([]byte(row.Content), &body))
			return h.m.checkProgressOn(claims(t, 2), tx, 1, row, body, &out)
		}))
		return out.Progress
	}
	before := read()
	require.NotNil(t, before.Computed)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.patchSettingsOn(claims(t, 3), tx, 1, 1, PlanningSettingsPatch{Timezone: settingPtr("America/Los_Angeles")})
		return err
	}))
	after := read()
	require.Equal(t, before, after, "settings edits must not remove same-period goal eligibility or B6 inputs")
}
