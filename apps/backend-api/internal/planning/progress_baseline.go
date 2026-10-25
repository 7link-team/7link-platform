package planning

import (
	"context"
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"gorm.io/gorm"
)

func validatePlanSchedule(items []PlanningPlanItem, start, end time.Time) error {
	for _, item := range items {
		if item.DueAt != nil && (item.DueAt.Before(start) || item.DueAt.After(end)) {
			return ErrInvalid
		}
	}
	return nil
}

func (m *Module) planReportProgressOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, report *model.Report, plan *model.Plan, start, end time.Time, out *PlanningReportProgress) (bool, error) {
	var body PlanningPlanInput
	if err := json.Unmarshal([]byte(plan.Content), &body); err != nil {
		return false, err
	}
	if len(body.Items) == 0 {
		return false, nil
	}
	for _, item := range body.Items {
		if item.DueAt == nil {
			return false, nil
		}
		if !validProgressWeight(item.Weight) {
			return false, ErrInvalid
		}
	}
	if err := validatePlanSchedule(body.Items, start, end); err != nil {
		return false, err
	}
	cutoff := minTime(m.now(), end)
	// Linked completion always comes from accepted work, never plan claims.
	facts, err := m.reportFactsOn(ctx, tx, tenant, report, start, end)
	if err != nil {
		return false, err
	}
	accepted := map[guid.ID]bool{}
	for _, work := range facts.Work {
		if work.State == "ACCEPTED" && work.AccountablePrincipalID == report.PrincipalID && work.AcceptedAt != nil && !work.AcceptedAt.Before(start) && work.AcceptedAt.Before(end) && !work.AcceptedAt.After(m.now()) {
			accepted[work.ID] = true
		}
	}
	var total, due, completed float64
	for _, item := range body.Items {
		weight := progressWeight(item.Weight)
		total += weight
		if !item.DueAt.After(cutoff) {
			due += weight
		}
		done := item.WorkItemID != nil && accepted[*item.WorkItemID]
		if item.WorkItemID == nil && item.CompletedAt != nil && !item.CompletedAt.Before(start) && item.CompletedAt.Before(end) && !item.CompletedAt.After(m.now()) && validText(item.CompletionEvidence, 2000) {
			done = true
		}
		if done {
			completed += weight
		}
	}
	out.Computed, out.Expected = progressValue(completed/total), progressValue(due/total)
	out.ExpectedSource = "PLAN_SCHEDULE"
	return true, nil
}

// Child weights are explicit goal target weights. Legacy goals without a
// weight are not silently interpreted as an aggregate weighting decision.
func (m *Module) childTargetWeightOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, child *model.Report) (float64, error) {
	start, end, err := reportPeriod(child)
	if err != nil {
		return 0, err
	}
	var body PlanningReportInput
	if err := json.Unmarshal([]byte(child.Content), &body); err != nil {
		return 0, err
	}
	q := query.BindGoals(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(child.ProjectID), q.State.In("ACTIVE", "AT_RISK", "ACHIEVED", "MISSED"))
	if child.PrincipalID > 0 {
		base = base.Where(q.PrincipalID.Eq(child.PrincipalID))
	}
	rows, err := base.Limit(501).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 500 {
		return 0, ErrUnavailable
	}
	var weight float64
	for _, row := range rows {
		_, goal, err := m.goalOn(ctx, tx, tenant, child.ProjectID, row.ID)
		if err != nil {
			return 0, err
		}
		if goal.Level != bodyScopeLevel(child) {
			continue
		}
		a, b, err := settingsPeriod(row.PeriodKind, row.PeriodStart, body.CalendarTimezone)
		if err != nil {
			return 0, err
		}
		if a.After(start) || b.Before(end) {
			continue
		}
		if goal.Weight <= 0 || !validProgressWeight(goal.Weight) {
			return 0, nil
		}
		weight += goal.Weight
	}
	return weight, nil
}

func (m *Module) childReportProgressOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, report *model.Report, children []*model.Report) (*float64, error) {
	var total, weights float64
	for _, child := range children {
		// Progress aggregates hierarchy peers in the same reporting period;
		// lower-period links are provenance, not duplicate target contributions.
		if child.PeriodKind != report.PeriodKind || child.PeriodStart != report.PeriodStart {
			continue
		}
		if (report.ProjectID > 0 && child.PrincipalID == 0) || (report.ProjectID == 0 && child.ProjectID == 0) {
			continue
		}
		progressChild := *child
		if report.ProjectID > 0 && progressChild.ProjectID == 0 {
			progressChild.ProjectID = report.ProjectID
		}
		weight, err := m.childTargetWeightOn(ctx, tx, tenant, &progressChild)
		if err != nil {
			return nil, err
		}
		if weight == 0 {
			return nil, nil
		}
		var body PlanningReportInput
		if err := json.Unmarshal([]byte(child.Content), &body); err != nil {
			return nil, err
		}
		check := PlanningCheck{Result: "PASS"}
		if err := m.checkProgressOn(ctx, tx, tenant, &progressChild, body, &check); err != nil {
			return nil, err
		}
		if check.Progress.Computed == nil {
			return nil, nil
		}
		total += weight * *check.Progress.Computed
		weights += weight
	}
	if weights == 0 {
		return nil, nil
	}
	return progressValue(total / weights), nil
}
