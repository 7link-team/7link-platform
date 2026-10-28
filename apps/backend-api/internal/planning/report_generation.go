package planning

import (
	activityi18n "7link.com/platform/internal/planning/i18n"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type reportMembershipHistory interface {
	MembershipIntervalsOn(context.Context, *gorm.DB, guid.ID, time.Time, time.Time) ([]core.MembershipInterval, error)
	MembershipObservedSinceOn(context.Context, *gorm.DB, guid.ID) (*time.Time, error)
}

func (m *Module) injectReportGeneration(s *module.Services) error {
	var err error
	m.planningCarryovers, err = module.Resolve[yunxiao.PlanningCarryovers](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.scheduledFacts, err = module.Resolve[delivery.ScheduledPlanningFacts](s)
	if err != nil {
		return err
	}
	cat, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return cat.AddTypes(Name, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_planning_report_generated_name", DescriptionKey: activityi18n.Namespace + ".activity_event_planning_report_generated_description", Name: "planning.report_generated", SchemaVersion: 1, SubjectKind: "planning_report", Realtime: events.RealtimeSignal})
}

// scheduledReportDue parses the configured due instant and calendar anchor
// for the period containing now, shared by report generation and loop timers.
func scheduledReportDue(kind string, settings PlanningSettings, now time.Time) (time.Time, time.Time, bool) {
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	anchor := day
	clock := settings.DayReportAt
	switch kind {
	case "DAY":
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if !slices.Contains(settings.Workdays, weekday) {
			return time.Time{}, time.Time{}, false
		}
	case "WEEK":
		weekday := (int(day.Weekday()) + 6) % 7
		anchor = day.AddDate(0, 0, -weekday)
		parts := strings.Fields(settings.WeekReportAt)
		if len(parts) != 2 {
			return time.Time{}, time.Time{}, false
		}
		offset := slices.Index([]string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}, parts[0])
		if offset < 0 {
			return time.Time{}, time.Time{}, false
		}
		day, clock = anchor.AddDate(0, 0, offset), parts[1]
	case "MONTH", "QUARTER", "YEAR":
		month := now.Month()
		if kind == "QUARTER" {
			month = time.Month((int(month)-1)/3*3 + 1)
		}
		if kind == "YEAR" {
			month = time.January
		}
		anchor = time.Date(now.Year(), month, 1, 0, 0, 0, 0, loc)
		schedule := map[string]string{"MONTH": settings.MonthReportAt, "QUARTER": settings.QuarterReportAt, "YEAR": settings.YearReportAt}[kind]
		parts := strings.Fields(schedule)
		if len(parts) != 2 {
			return time.Time{}, time.Time{}, false
		}
		n, err := strconv.Atoi(strings.TrimPrefix(parts[0], "D"))
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		day, clock = anchor.AddDate(0, 0, n-1), parts[1]
	default:
		return time.Time{}, time.Time{}, false
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	due := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
	return due, anchor, true
}

func scheduledReportPeriod(kind string, settings PlanningSettings, now time.Time) (string, bool) {
	due, anchor, ok := scheduledReportDue(kind, settings, now)
	if !ok || now.Before(due) {
		return "", false
	}

	switch kind {
	case "WEEK":
		anchor = anchor.AddDate(0, 0, -7)
	case "MONTH":
		anchor = anchor.AddDate(0, -1, 0)
	case "QUARTER":
		anchor = anchor.AddDate(0, -3, 0)
	case "YEAR":
		anchor = anchor.AddDate(-1, 0, 0)
	}
	return anchor.Format("2006-01-02"), true
}

// nextScheduledReportAt shares report calendar semantics with recurring loops.
// It returns the first configured due instant strictly after the observation.
func nextScheduledReportAt(kind string, settings PlanningSettings, now time.Time) (time.Time, bool) {
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return time.Time{}, false
	}
	candidate := now.In(loc)
	for attempts := 0; attempts < 8; attempts++ {
		due, anchor, ok := scheduledReportDue(kind, settings, candidate)
		if ok && due.After(now) {
			return due, true
		}
		if !ok && kind != "DAY" {
			return time.Time{}, false
		}
		switch kind {
		case "DAY":
			candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()+1, 0, 0, 0, 0, loc)
		case "WEEK":
			candidate = anchor.AddDate(0, 0, 7)
		case "MONTH":
			candidate = anchor.AddDate(0, 1, 0)
		case "QUARTER":
			candidate = anchor.AddDate(0, 3, 0)
		case "YEAR":
			candidate = anchor.AddDate(1, 0, 0)
		default:
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}

func membershipOverlapsWorkday(interval core.MembershipInterval, start, end, now time.Time, loc *time.Location, workdays []int) bool {
	a, b := start, minTime(end, now)
	if interval.Start.After(a) {
		a = interval.Start
	}
	if interval.End != nil && interval.End.Before(b) {
		b = *interval.End
	}
	if !a.Before(b) {
		return false
	}
	local := a.In(loc)
	for day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc); day.Before(b); day = day.AddDate(0, 0, 1) {
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		if slices.Contains(workdays, weekday) && day.AddDate(0, 0, 1).After(a) {
			return true
		}
	}
	return false
}

func (m *Module) generateReports(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	history, ok := m.members.(reportMembershipHistory)
	if !ok || m.scheduledFacts.ProjectsOn == nil || m.scheduledFacts.ReadOn == nil || m.principals.ActiveHumanByID == nil || m.principals.LockActiveRow == nil || m.audit == nil || m.events == nil || m.ids == nil {
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
		cutoff, err := history.MembershipObservedSinceOn(ctx, tx, tenant)
		if err != nil {
			return err
		}
		if cutoff == nil {
			return nil
		}
		loc, err := time.LoadLocation(settings.Timezone)
		if err != nil {
			return err
		}
		for _, kind := range []string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"} {
			date, ready := scheduledReportPeriod(kind, settings, m.now())
			if !ready {
				continue
			}
			start, end, err := settingsPeriod(kind, date, settings.Timezone)
			if err != nil {
				return err
			}
			// Earlier periods have unknowable pre-migration membership, so a
			// partial historical roster must never masquerade as complete.
			if start.Before(*cutoff) {
				continue
			}
			projects, err := m.scheduledFacts.ProjectsOn(ctx, tx, tenant, start, end)
			if err != nil {
				return err
			}
			intervals, err := history.MembershipIntervalsOn(ctx, tx, tenant, start, end)
			if err != nil {
				return err
			}
			members := []guid.ID{}
			if slices.Contains(settings.RequiredMemberKinds, kind) {
				for _, interval := range intervals {
					if slices.Contains(members, interval.PrincipalID) || !membershipOverlapsWorkday(interval, start, end, m.now(), loc, settings.Workdays) {
						continue
					}
					p, human, err := m.principals.ActiveHumanByID(ctx, tx, interval.PrincipalID)
					if err != nil {
						return err
					}
					if human && p.ID == interval.PrincipalID {
						members = append(members, interval.PrincipalID)
					}
				}
			}
			slices.Sort(members)
			for _, subject := range members {
				p, active, err := m.principals.LockActiveRow(ctx, tx, subject)
				if err != nil {
					return err
				}
				if !active || p.ID != subject {
					continue
				}
				if err := m.generateReportOn(ctx, tx, tenant, 0, subject, kind, date, settings, projects, start, end); err != nil {
					return err
				}
			}
			for _, project := range projects {
				if err := m.generateReportOn(ctx, tx, tenant, project, 0, kind, date, settings, []guid.ID{project}, start, end); err != nil {
					return err
				}
			}
			if err := m.generateReportOn(ctx, tx, tenant, 0, 0, kind, date, settings, projects, start, end); err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Module) generateReportOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, kind, date string, settings PlanningSettings, projects []guid.ID, start, end time.Time) error {
	q := query.BindReports(tx, m.names)
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.PrincipalID.Eq(subject), q.PeriodKind.Eq(kind), q.PeriodStart.Eq(date)).Count()
	if err != nil || count > 0 {
		return err
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	scope := "TEAM"
	if project > 0 {
		scope = "PROJECT"
	}
	if subject > 0 {
		scope = "MEMBER"
	}
	body := PlanningReportInput{CalendarTimezone: settings.Timezone, Scope: scope, BoundaryStart: &start, BoundaryEnd: &end, Completed: []PlanningCompletion{}, Children: []PlanningChildReportRef{}, NextPlan: []PlanningPlanItem{}}
	facts := delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{}, MissingSources: []string{"AGENT_MEMBERSHIP_HISTORY", "HISTORICAL_PRINCIPAL_KIND", "SYSTEM_TEMPLATE_DRAFT"}}
	seen := map[guid.ID]bool{}
	for _, source := range projects {
		if m.planningCarryovers.ReadOn != nil {
			carryovers, e := m.planningCarryovers.ReadOn(ctx, tx, tenant, source, subject, start, end)
			if e != nil {
				return e
			}
			for _, fact := range carryovers {
				body.Uncompleted += fmt.Sprintf("CARRIED_OVER work_item_id=%s from_iteration_id=%s\n", fact.WorkItemID.String(), fact.FromIterationID.String())
			}
		}
		part, err := m.scheduledFacts.ReadOn(ctx, tx, tenant, source, subject, start, end)
		if err != nil {
			return err
		}
		if subject == 0 {
			if err := m.hydrateReportQualityOn(ctx, tx, tenant, source, start, end, &part); err != nil {
				return err
			}
		} else if m.memberQuality != nil {
			scope, e := m.scheduledFacts.ReadOn(ctx, tx, tenant, source, 0, start, end)
			if e != nil {
				return e
			}
			if e = m.hydrateMemberQualityOn(ctx, tx, tenant, source, subject, start, end, scope, &part); e != nil {
				return e
			}
		}
		for _, missing := range part.MissingSources {
			if !slices.Contains(facts.MissingSources, missing) {
				facts.MissingSources = append(facts.MissingSources, missing)
			}
		}
		if part.QualityMetrics != nil {
			if facts.QualityMetrics == nil {
				facts.QualityMetrics = &delivery.ReportQualityMetrics{}
			}
			facts.QualityMetrics.Add(part.QualityMetrics)
		}
		for _, work := range part.Work {
			if seen[work.ID] {
				continue
			}
			seen[work.ID] = true
			facts.Work = append(facts.Work, work)
			if work.State == "ACCEPTED" && work.AcceptedAt != nil && !work.AcceptedAt.Before(start) && work.AcceptedAt.Before(end) && !work.AcceptedAt.After(m.now()) {
				body.Completed = append(body.Completed, PlanningCompletion{WorkItemID: work.ID})
			} else if subject > 0 && work.AccountablePrincipalID == subject && slices.Contains([]string{"READY", "CLAIMED", "IN_PROGRESS", "REWORK", "BLOCKED", "NEEDS_HUMAN_REVIEW"}, work.State) {
				reason := "OTHER"
				if work.State == "BLOCKED" {
					reason = "BLOCKED_DEPENDENCY"
				}
				if work.State == "REWORK" {
					reason = "QUALITY_REWORK"
				}
				id := work.ID
				body.UncompletedItems = append(body.UncompletedItems, PlanningUncompleted{Key: work.ID.String(), Reason: reason, Detail: work.State, CarryOver: "CARRY_TO_NEXT", Item: PlanningPlanItem{Title: work.Title, WorkItemID: &id}})
			}
		}
	}
	if slices.Contains(facts.MissingSources, "QUALITY_METRICS") {
		facts.QualityMetrics = nil
	}
	if subject == 0 {
		children := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PeriodKind.Eq(kind), q.PeriodStart.Eq(date))
		if project > 0 {
			// MEMBER identity is tenant-wide. Project provenance includes only
			// members with actual work in the project, never invented assignments.
			contributors := []guid.ID{}
			for _, work := range facts.Work {
				if work.AccountablePrincipalID > 0 {
					contributors = append(contributors, work.AccountablePrincipalID)
				}
			}
			rows, err := children.Where(q.ProjectID.Eq(guid.ID(0)), q.PrincipalID.Gt(guid.ID(0))).Find()
			if err != nil {
				return err
			}
			for _, child := range rows {
				if slices.Contains(contributors, child.PrincipalID) {
					body.Children = append(body.Children, PlanningChildReportRef{ID: child.ID, ContentHash: child.ContentHash})
				}
			}
		} else {
			rows, err := children.Where(q.PrincipalID.Eq(guid.ID(0)), q.ProjectID.Gt(guid.ID(0))).Find()
			if err != nil {
				return err
			}
			for _, child := range rows {
				body.Children = append(body.Children, PlanningChildReportRef{ID: child.ID, ContentHash: child.ContentHash})
			}
			// Start a fresh builder: the earlier project-child query has already
			// applied project/principal filters to its GORM statement.
			unassigned, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.PeriodKind.Eq(kind), q.PeriodStart.Eq(date), q.ProjectID.Eq(guid.ID(0)), q.PrincipalID.Gt(guid.ID(0))).Find()
			if err != nil {
				return err
			}
			for _, child := range unassigned {
				if !slices.ContainsFunc(facts.Work, func(work delivery.PlanningWorkFact) bool { return work.AccountablePrincipalID == child.PrincipalID }) {
					body.Children = append(body.Children, PlanningChildReportRef{ID: child.ID, ContentHash: child.ContentHash})
				}
			}
		}
	}
	r := &model.Report{TenantID: tenant, ID: guid.ID(id), ProjectID: project, PrincipalID: subject, PeriodKind: kind, PeriodStart: date, State: "DRAFT", Version: 1, CreatedBy: 0, CreatedAt: m.now(), UpdatedAt: m.now()}
	r.Content, r.ContentHash, err = hashJSON(body)
	if err != nil {
		return err
	}
	r.Facts, _, err = hashJSON(facts)
	if err != nil {
		return err
	}
	if err = m.initializeReportDueOn(ctx, tx, tenant, r); err != nil {
		return err
	}
	// The report-key uniqueness constraint is the final concurrent replay
	// fence; audit/outbox are emitted only for the insertion that won.
	result := tx.WithContext(ctx).Table(m.names.Reports.String()).Clauses(clause.OnConflict{DoNothing: true}).Create(r)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if err = m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "planning.report_generated", TargetType: "planning_report", TargetID: r.ID, Detail: map[string]any{"authority_source": "scheduled:planning.report_generate", "actor_kind": "SYSTEM", "scope": scope, "missing_sources": facts.MissingSources}}); err != nil {
		return err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "planning.report_generated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "scheduled:planning.report_generate"}, Subject: events.Subject{Kind: "planning_report", ID: r.ID}, SubjectVersion: 1, Data: map[string]any{"scope": scope, "period_kind": kind, "period_start": date, "missing_sources": facts.MissingSources}})
	return err
}
