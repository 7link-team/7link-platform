package planning

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"gorm.io/gorm"
)

// Nil means the corresponding source is absent, never zero completion.
// Legacy plans without schedules remain explicit missing sources.
type PlanningReportProgress struct {
	Computed       *float64 `json:"computed_progress,omitempty"`
	Expected       *float64 `json:"expected_progress,omitempty"`
	Lag            *float64 `json:"progress_lag,omitempty"`
	ExpectedSource string   `json:"expected_source,omitempty"`
	MissingSources []string `json:"missing_sources"`
}

func progressValue(v float64) *float64 {
	v = math.Round(v*10000) / 10000
	return &v
}

// Count local calendar days, including the elapsed fraction of today's local
// day. Calendar increments preserve the configured weekdays across DST.
func elapsedWorkdays(start, end, now time.Time, loc *time.Location, workdays []int) *float64 {
	if !start.Before(end) || len(workdays) == 0 {
		return nil
	}
	var total, elapsed float64
	for day := start.In(loc); day.Before(end); day = day.AddDate(0, 0, 1) {
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if !slices.Contains(workdays, weekday) {
			continue
		}
		next := day.AddDate(0, 0, 1)
		total++
		if !now.Before(next) {
			elapsed++
		} else if now.After(day) {
			elapsed += now.Sub(day).Seconds() / next.Sub(day).Seconds()
		}
	}
	if total == 0 {
		return nil
	}
	return progressValue(elapsed / total)
}

func (m *Module) checkProgressOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, body PlanningReportInput, out *PlanningCheck) error {
	progress := PlanningReportProgress{MissingSources: []string{}}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	start, end, err := reportPeriod(r)
	if err != nil {
		return err
	}
	// Submitted plans are the canonical baseline. Legacy items without due
	// dates retain an explicit missing source instead of a calendar fallback.
	p := query.BindPlans(tx, m.names)
	planQuery := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.PrincipalID.Eq(r.PrincipalID), p.PeriodKind.Eq(r.PeriodKind), p.PeriodStart.Eq(r.PeriodStart), p.State.In("SUBMITTED", "CONFIRMED"))
	if r.ProjectID > 0 || r.PrincipalID == 0 {
		planQuery = planQuery.Where(p.ProjectID.Eq(r.ProjectID))
	}
	plans, err := planQuery.Limit(501).Find()
	if err != nil {
		return err
	}
	if len(plans) > 500 {
		return ErrUnavailable
	}
	if len(plans) > 0 {
		baseline := *plans[0]
		if len(plans) > 1 {
			combined := PlanningPlanInput{}
			for _, plan := range plans {
				var body PlanningPlanInput
				if err := json.Unmarshal([]byte(plan.Content), &body); err != nil {
					return err
				}
				combined.Items = append(combined.Items, body.Items...)
			}
			baseline.Content, _, err = hashJSON(combined)
			if err != nil {
				return err
			}
		}
		available, err := m.planReportProgressOn(ctx, tx, tenant, r, &baseline, start, end, &progress)
		if err != nil {
			return err
		}
		if !available {
			progress.MissingSources = append(progress.MissingSources, "PLAN_PROGRESS_BASELINE")
		}
	} else {
		timezone := body.CalendarTimezone
		if timezone == "" {
			timezone = settings.Timezone
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return err
		}
		progress.Expected = elapsedWorkdays(start, end, m.now(), loc, settings.Workdays)
		if progress.Expected == nil {
			progress.MissingSources = append(progress.MissingSources, "WORKDAY_CALENDAR")
		} else {
			progress.ExpectedSource = "ELAPSED_WORKDAYS"
		}
		// Aggregation needs actual target weights for submitted children; a
		// simple average must not silently substitute for that contract.
		var children []*model.Report
		if r.PrincipalID == 0 {
			children, err = m.childReportsOn(ctx, tx, tenant, r)
			if err != nil {
				return err
			}
			children = slices.DeleteFunc(children, func(child *model.Report) bool {
				return child.State != "SUBMITTED" && child.State != "CONFIRMED"
			})
		}
		if len(children) > 0 {
			progress.Computed, err = m.childReportProgressOn(ctx, tx, tenant, r, children)
			if err != nil {
				return err
			}
			if progress.Computed == nil {
				progress.MissingSources = append(progress.MissingSources, "CHILD_PROGRESS_WEIGHTS")
			}
		} else {
			progress.Computed, err = m.goalReportProgressOn(ctx, tx, tenant, r, start, end, timezone)
			if err != nil {
				return err
			}
			if progress.Computed == nil {
				progress.MissingSources = append(progress.MissingSources, "GOAL_PROGRESS")
			}
		}
	}
	if progress.Expected != nil && progress.Computed != nil {
		progress.Lag = progressValue(*progress.Expected - *progress.Computed)
		if body.DeclaredConfidence == "GREEN" && *progress.Lag >= settings.WatermelonLagThreshold {
			blocking := utf8.RuneCountInString(strings.TrimSpace(body.StatusExplanation)) < 20
			code := "N5_STATUS_EXPLAINED"
			if blocking {
				code = "B6_WATERMELON"
			}
			out.Issues = append(out.Issues, PlanningIssue{Code: code, Blocking: blocking})
			if blocking {
				out.Result = "BLOCKED"
			} else if out.Result == "PASS" {
				out.Result = "PASS_WITH_FLAGS"
			}
		}
	}
	out.Progress = progress
	return nil
}

func (m *Module) goalReportProgressOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report, start, end time.Time, timezone string) (*float64, error) {
	q := query.BindGoals(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("ACTIVE", "AT_RISK", "ACHIEVED", "MISSED"), q.PeriodStart.Lte(r.PeriodStart))
	if r.ProjectID > 0 || r.PrincipalID == 0 {
		base = base.Where(q.ProjectID.Eq(r.ProjectID))
	}
	if r.PrincipalID > 0 {
		base = base.Where(q.PrincipalID.Eq(r.PrincipalID))
	}
	rows, err := base.Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	var total, weights float64
	level := bodyScopeLevel(r)
	for _, candidate := range rows {
		gs, ge, err := settingsPeriod(candidate.PeriodKind, candidate.PeriodStart, timezone)
		if err != nil {
			return nil, err
		}
		if gs.After(start) || ge.Before(end) {
			continue
		}
		row, goal, err := m.goalOn(ctx, tx, tenant, candidate.ProjectID, candidate.ID)
		if err != nil {
			return nil, err
		}
		if goal.Level != level || (r.PrincipalID > 0 && row.PrincipalID != r.PrincipalID) {
			continue
		}
		// AUTO values are refreshed from actual acceptance facts, not cached
		// current values and never a report author's completion claims.
		for i, kr := range goal.KeyResults {
			if kr.Source != "AUTO" {
				continue
			}
			if kr.Metric != "ACCEPTED_WORK_ITEMS" || candidate.ProjectID <= 0 {
				return nil, nil
			}
			facts, err := m.snapshotOn(ctx, tx, tenant, candidate.ProjectID, r.PrincipalID, gs, ge)
			if err != nil {
				return nil, err
			}
			accepted := map[guid.ID]bool{}
			cutoff := minTime(m.now(), minTime(end, ge))
			for _, work := range facts.Work {
				if work.State == "ACCEPTED" && work.AcceptedAt != nil && !work.AcceptedAt.Before(gs) && work.AcceptedAt.Before(cutoff) {
					accepted[work.ID] = true
				}
			}
			goal.KeyResults[i].Current = int64(len(accepted))
		}
		weight := progressWeight(goal.Weight)
		total += weight * goalProgress(goal)
		weights += weight
	}
	if weights == 0 {
		return nil, nil
	}
	return progressValue(total / weights), nil
}

func bodyScopeLevel(r *model.Report) string {
	var body PlanningReportInput
	if json.Unmarshal([]byte(r.Content), &body) != nil {
		return ""
	}
	if r.PrincipalID > 0 {
		return "MEMBER"
	}
	if r.ProjectID > 0 {
		return "PROJECT"
	}
	return "TEAM"
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func progressWeight(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}
