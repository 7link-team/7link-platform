package planning

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validUncompleted(items []PlanningUncompleted) bool {
	if len(items) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !validText(item.Key, 64) || seen[item.Key] || !slices.Contains([]string{"BLOCKED_DEPENDENCY", "SCOPE_CHANGED", "UNDERESTIMATED", "REPRIORITIZED", "WAITING_CLIENT", "QUALITY_REWORK", "RESOURCE", "OTHER"}, item.Reason) || !validText(item.Detail, 2000) || !validText(item.Item.Title, 200) || !slices.Contains([]string{"CARRY_TO_NEXT", "DROP", "ESCALATE"}, item.CarryOver) {
			return false
		}
		seen[item.Key] = true
		if item.Item.WorkItemID != nil && *item.Item.WorkItemID <= 0 {
			return false
		}
		if item.Item.GoalID < 0 || item.Item.ID < 0 || !validPlanAnnotations(item.Item) || !validProgressWeight(item.Item.Weight) {
			return false
		}
	}
	return true
}

func (m *Module) validatePlanItemsOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, items []PlanningPlanItem) error {
	if len(items) < 1 || len(items) > 200 {
		return ErrInvalid
	}
	for _, item := range items {
		scope := project
		if scope == 0 {
			q := query.BindGoals(tx, m.names)
			goal, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.GoalID)).First()
			if err != nil {
				return err
			}
			scope = goal.ProjectID
		}
		if err := m.validateItems(ctx, tx, tenant, scope, subject, []PlanningPlanItem{item}); err != nil {
			return err
		}
		if item.CarriedFromReportID == nil {
			if item.CarriedFromKey != "" || item.CarriedFromHash != "" || item.CarriedFromItemID != nil {
				return ErrInvalid
			}
			continue
		}
		q := query.BindReports(tx, m.names)
		source, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*item.CarriedFromReportID), q.PrincipalID.Eq(subject)).First()
		if err != nil {
			return err
		}
		if source.ContentHash != item.CarriedFromHash || !slices.Contains([]string{"SUBMITTED", "CONFIRMED"}, source.State) {
			return ErrConflict
		}
		var body PlanningReportInput
		if err := json.Unmarshal([]byte(source.Content), &body); err != nil {
			return err
		}
		found := false
		for _, previous := range body.UncompletedItems {
			if previous.Key == item.CarriedFromKey && previous.CarryOver == "CARRY_TO_NEXT" && (previous.Item.WorkItemID != nil) == (item.WorkItemID != nil) {
				found = previous.Item.WorkItemID == nil || *previous.Item.WorkItemID == *item.WorkItemID
				if previous.Item.ID > 0 {
					found = found && item.CarriedFromItemID != nil && *item.CarriedFromItemID == previous.Item.ID
				} else {
					found = found && item.CarriedFromItemID == nil
				}
			}
		}
		if !found {
			return ErrInvalid
		}
	}
	return nil
}

func (m *Module) validatePlanCarryPeriodOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, kind, date string, items []PlanningPlanItem) error {
	q := query.BindReports(tx, m.names)
	for _, item := range items {
		if item.CarriedFromReportID == nil {
			continue
		}
		source, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*item.CarriedFromReportID)).First()
		if err != nil {
			return err
		}
		_, end, err := period(source.PeriodKind, source.PeriodStart)
		if err != nil {
			return err
		}
		if source.PeriodKind != kind || end.Format("2006-01-02") != date {
			return ErrInvalid
		}
	}
	return nil
}

func (m *Module) reportPlansConfirmedOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, report *model.Report, end time.Time) (bool, error) {
	// Aggregate reports have no single member plan. Their child checks retain
	// each member's advisories; never invent a plan for principal zero.
	if report.PrincipalID == 0 {
		return true, nil
	}
	q := query.BindPlans(tx, m.names)
	for _, date := range []string{report.PeriodStart, end.In(mustReportLocation(report)).Format("2006-01-02")} {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(report.PrincipalID), q.PeriodKind.Eq(report.PeriodKind), q.PeriodStart.Eq(date), q.ProjectID.In(guid.ID(0), report.ProjectID)).Find()
		if err != nil {
			return false, err
		}
		// A project plan takes precedence over the canonical tenant member plan.
		var chosen *model.Plan
		for _, row := range rows {
			if chosen == nil || row.ProjectID == report.ProjectID {
				chosen = row
			}
		}
		if chosen == nil || chosen.State != "CONFIRMED" {
			return false, nil
		}
	}
	return true, nil
}

func mustReportLocation(report *model.Report) *time.Location {
	var body PlanningReportInput
	if json.Unmarshal([]byte(report.Content), &body) == nil && body.CalendarTimezone != "" {
		if loc, err := time.LoadLocation(body.CalendarTimezone); err == nil {
			return loc
		}
	}
	return time.UTC
}

// scheduledPlanPeriod follows BR-10-15's explicit local 08:00 examples.
// A missed scan catches up within the current period, never replacing a draft.
func scheduledPlanPeriod(kind string, settings PlanningSettings, now time.Time) (string, bool) {
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return "", false
	}
	n := now.In(loc)
	day := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	anchor := day
	switch kind {
	case "DAY":
		weekday := (int(day.Weekday())+6)%7 + 1
		if !slices.Contains(settings.Workdays, weekday) {
			return "", false
		}
	case "WEEK":
		anchor = day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	case "MONTH":
		anchor = time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	case "QUARTER":
		anchor = time.Date(n.Year(), time.Month((int(n.Month())-1)/3*3+1), 1, 0, 0, 0, 0, loc)
	case "YEAR":
		anchor = time.Date(n.Year(), 1, 1, 0, 0, 0, 0, loc)
	default:
		return "", false
	}
	due := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 8, 0, 0, 0, loc)
	return anchor.Format("2006-01-02"), !now.Before(due)
}

func (m *Module) generatePlans(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	history, ok := m.members.(reportMembershipHistory)
	if !ok || m.ids == nil || m.audit == nil || m.events == nil || m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.scheduledFacts.ProjectsOn == nil || m.scheduledFacts.ReadOn == nil {
		return ErrUnavailable
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		settings, err := m.readSettingsOn(ctx, tx, tenant)
		if err != nil {
			return err
		}
		now := m.now()
		intervals, err := history.MembershipIntervalsOn(ctx, tx, tenant, now, now.Add(time.Millisecond))
		if err != nil {
			return err
		}
		subjects := []guid.ID{}
		for _, interval := range intervals {
			if interval.PrincipalID > 0 && !interval.Start.After(now) && (interval.End == nil || interval.End.After(now)) {
				subjects = append(subjects, interval.PrincipalID)
			}
		}
		slices.Sort(subjects)
		for _, subject := range slices.Compact(subjects) {
			p, active, err := m.principals.LockActiveRow(ctx, tx, subject)
			if err != nil {
				return err
			}
			if !active || p.ID != subject {
				continue
			}
			active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, subject)
			if err != nil {
				return err
			}
			if !active {
				continue
			}
			human, isHuman, err := m.principals.ActiveHumanByID(ctx, tx, subject)
			if err != nil {
				return err
			}
			if !isHuman || human.ID != subject {
				if m.agents.ByPrincipalOn == nil {
					return ErrUnavailable
				}
				agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, subject)
				if err != nil {
					return err
				}
				if !found || agent.TenantID != tenant || agent.PrincipalID != subject || agent.State != "ACTIVE" {
					continue
				}
			}
			for _, kind := range []string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"} {
				date, ready := scheduledPlanPeriod(kind, settings, now)
				if ready {
					if err := m.generatePlanOn(ctx, tx, tenant, subject, kind, date, settings); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (m *Module) draftSuggestionsOn(ctx context.Context, tx *gorm.DB, tenant, subject guid.ID, start, end time.Time, settings PlanningSettings) ([]PlanningPlanItem, error) {
	q := query.BindGoals(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("ACTIVE", "AT_RISK")).Order(q.ID).Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrUnavailable
	}
	out := []PlanningPlanItem{}
	for _, row := range rows {
		a, b, err := settingsPeriod(row.PeriodKind, row.PeriodStart, settings.Timezone)
		if err != nil {
			return nil, err
		}
		if !a.Before(end) || !start.Before(b) {
			continue
		}
		var goal PlanningGoalInput
		if err := json.Unmarshal([]byte(row.Content), &goal); err != nil {
			return nil, err
		}
		var allowed authz.DataConstraint
		if row.ProjectID > 0 {
			if m.entityPolicy == nil {
				return nil, ErrUnavailable
			}
			allowed, err = m.entityPolicy(ctx, tx, tenant, row.ProjectID, "goal_read", subject)
			if err == nil && !allowed.AllowsEntityRows(row.ProjectID) {
				continue
			}
		} else {
			if m.policy == nil {
				return nil, ErrUnavailable
			}
			allowed, err = m.policy(ctx, tx, tenant, "goal_read", subject)
		}
		if err != nil {
			return nil, err
		}
		if !allowed.AllowAll && !slices.Contains(allowed.OwnerIDs, row.PrincipalID.String()) && !slices.Contains(allowed.ResourceIDs, row.ProjectID.String()) {
			continue
		}
		for _, kr := range goal.KeyResults {
			if (kr.Target > kr.Baseline && kr.Current >= kr.Target) || (kr.Target < kr.Baseline && kr.Current <= kr.Target) {
				continue
			}
			contributes, err := m.contributesOn(ctx, tx, tenant, row, goal, subject, kr.Key)
			if err != nil {
				return nil, err
			}
			if !contributes {
				continue
			}
			out = append(out, PlanningPlanItem{Title: kr.Title, GoalID: row.ID, KR: kr.Key, Category: "OTHER"})
			if len(out) > 100 {
				return nil, ErrUnavailable
			}
		}
	}
	return out, nil
}

func (m *Module) generatePlanOn(ctx context.Context, tx *gorm.DB, tenant, subject guid.ID, kind, date string, settings PlanningSettings) error {
	q := query.BindPlans(tx, m.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(guid.ID(0)), q.PrincipalID.Eq(subject), q.PeriodKind.Eq(kind), q.PeriodStart.Eq(date)).Count()
	if err != nil || n > 0 {
		return err
	}
	start, end, err := settingsPeriod(kind, date, settings.Timezone)
	if err != nil {
		return err
	}
	body := PlanningPlanInput{Items: []PlanningPlanItem{}}
	body.Suggestions, err = m.draftSuggestionsOn(ctx, tx, tenant, subject, start, end, settings)
	if err != nil {
		return err
	}
	projects, err := m.scheduledFacts.ProjectsOn(ctx, tx, tenant, m.now(), m.now().Add(time.Millisecond))
	if err != nil {
		return err
	}
	work := map[guid.ID]delivery.PlanningWorkFact{}
	ordered := []guid.ID{}
	for _, project := range projects {
		part, err := m.scheduledFacts.ReadOn(ctx, tx, tenant, project, subject, time.Unix(0, 0).UTC(), m.now().Add(time.Millisecond))
		if err != nil {
			return err
		}
		for _, fact := range part.Work {
			if fact.AccountablePrincipalID != subject || !slices.Contains([]string{"READY", "CLAIMED", "IN_PROGRESS", "REWORK", "BLOCKED", "NEEDS_HUMAN_REVIEW"}, fact.State) {
				continue
			}
			if _, exists := work[fact.ID]; !exists && slices.Contains([]string{"READY", "CLAIMED", "IN_PROGRESS", "REWORK"}, fact.State) {
				ordered = append(ordered, fact.ID)
			}
			work[fact.ID] = fact
		}
	}
	seen := map[guid.ID]bool{}
	r := query.BindReports(tx, m.names)
	previousStart := start
	switch kind {
	case "DAY":
		previousStart = start.AddDate(0, 0, -1)
	case "WEEK":
		previousStart = start.AddDate(0, 0, -7)
	case "MONTH":
		previousStart = start.AddDate(0, -1, 0)
	case "QUARTER":
		previousStart = start.AddDate(0, -3, 0)
	case "YEAR":
		previousStart = start.AddDate(-1, 0, 0)
	}
	reports, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.PrincipalID.Eq(subject), r.PeriodKind.Eq(kind), r.PeriodStart.Eq(previousStart.Format("2006-01-02")), r.State.In("SUBMITTED", "CONFIRMED")).Order(r.ID).Limit(501).Find()
	if err != nil {
		return err
	}
	if len(reports) > 500 {
		return ErrUnavailable
	}
	for _, report := range reports {
		_, previousEnd, err := settingsPeriod(kind, report.PeriodStart, settings.Timezone)
		if err != nil {
			return err
		}
		if !previousEnd.Equal(start) {
			continue
		}
		var previous PlanningReportInput
		if err := json.Unmarshal([]byte(report.Content), &previous); err != nil {
			return err
		}
		if !validUncompleted(previous.UncompletedItems) {
			return ErrInvalid
		}
		for _, unfinished := range previous.UncompletedItems {
			if unfinished.CarryOver != "CARRY_TO_NEXT" {
				continue
			}
			item := unfinished.Item
			item.CarriedFromItemID = nil
			if item.ID > 0 {
				sourceID := item.ID
				item.CarriedFromItemID = &sourceID
			}
			item.ID = 0
			if item.WorkItemID != nil {
				if _, exists := work[*item.WorkItemID]; !exists || seen[*item.WorkItemID] {
					continue
				}
				seen[*item.WorkItemID] = true
			}
			// Source goal links are retained only while contribution is current.
			if item.GoalID > 0 && !slices.ContainsFunc(body.Suggestions, func(s PlanningPlanItem) bool { return s.GoalID == item.GoalID && s.KR == item.KR }) {
				item.GoalID = 0
				item.KR = ""
			}
			id := report.ID
			item.CarriedFromReportID = &id
			item.CarriedFromKey = unfinished.Key
			item.CarriedFromHash = report.ContentHash
			item.DueAt = nil
			item.CompletedAt = nil
			item.CompletionEvidence = ""
			body.Items = append(body.Items, item)
		}
	}
	for _, id := range ordered {
		if !seen[id] {
			fact := work[id]
			workID := id
			body.Items = append(body.Items, PlanningPlanItem{Title: fact.Title, WorkItemID: &workID})
		}
	}
	if err := validatePlanLimit(kind, body.Items); err != nil {
		return err
	}
	if err := m.assignPlanItemIDs(body.Items, nil); err != nil {
		return err
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	content, hash, err := hashJSON(body)
	if err != nil {
		return err
	}
	row := &model.Plan{TenantID: tenant, ID: guid.ID(id), PrincipalID: subject, PeriodKind: kind, PeriodStart: date, State: "DRAFT", Content: content, ContentHash: hash, Version: 1, CreatedBy: 0, CreatedAt: m.now(), UpdatedAt: m.now()}
	result := tx.WithContext(ctx).Table(m.names.Plans.String()).Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if err := m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "planning.changed", TargetType: "planning_record", TargetID: row.ID, Detail: map[string]any{"authority_source": "scheduled:planning.plan_draft", "actor_kind": "SYSTEM", "action": "DRAFT", "kind": "plan"}}); err != nil {
		return err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "planning.changed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "scheduled:planning.plan_draft"}, Subject: events.Subject{Kind: "planning_record", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"kind": "plan", "action": "DRAFT", "period_kind": kind, "period_start": date}})
	return err
}
