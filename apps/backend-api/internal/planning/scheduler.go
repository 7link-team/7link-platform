package planning

import (
	activityi18n "7link.com/platform/internal/planning/i18n"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) injectScheduler(s *module.Services) error {
	if source, err := module.Resolve[identity.NotificationCandidates](s); err == nil {
		m.notificationCandidates = source
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err := m.bindNotificationSource(s); err != nil {
		return err
	}
	var err error
	m.reportNotifier, err = module.Resolve[notification.Notifier](s)
	return err
}
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{DisplayNameKey: activityi18n.Namespace + ".activity_job_planning_report_overdue_name", DescriptionKey: activityi18n.Namespace + ".activity_job_planning_report_overdue_description", Kind: "planning.report_overdue", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if err := m.scanOverdue(ctx, job.TenantID); err != nil {
			return err
		}
		return m.scanNotificationReminders(ctx, job.TenantID)
	}}, {DisplayNameKey: activityi18n.Namespace + ".activity_job_planning_report_generate_name", DescriptionKey: activityi18n.Namespace + ".activity_job_planning_report_generate_description", Kind: "planning.report_generate", Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error { return m.generateReports(ctx, job.TenantID) }}, m.loopTimeoutJobHandler(), {DisplayNameKey: activityi18n.Namespace + ".activity_job_planning_goal_refresh_name", DescriptionKey: activityi18n.Namespace + ".activity_job_planning_goal_refresh_description", Kind: "planning.goal_refresh", Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error { return m.refreshGoals(ctx, job.TenantID) }}, {DisplayNameKey: activityi18n.Namespace + ".activity_job_planning_plan_draft_name", DescriptionKey: activityi18n.Namespace + ".activity_job_planning_plan_draft_description", Kind: "planning.plan_draft", Timeout: 5 * time.Minute, Run: func(ctx context.Context, job jobs.Job) error { return m.generatePlans(ctx, job.TenantID) }}}
}
func (m *Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "planning.report_overdue", Kind: "planning.report_overdue", Interval: 10 * time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, {Name: "planning.report_generate", Kind: "planning.report_generate", Interval: 5 * time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, m.loopTimeoutJobSchedule(), {Name: "planning.goal_refresh", Kind: "planning.goal_refresh", Interval: 15 * time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}, {Name: "planning.plan_draft", Kind: "planning.plan_draft", Interval: 5 * time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireCoalesce, Payload: map[string]any{}}}
}

func (m *Module) initializeReportDueOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.Report) error {
	if r == nil || r.TenantID != tenant {
		return ErrInvalid
	}
	_, end, err := reportPeriod(r)
	if err != nil {
		return err
	}
	settings, err := m.readSettingsOn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	grace, ok := map[string]int{"DAY": settings.GraceDayMinutes, "WEEK": settings.GraceWeekMinutes, "MONTH": settings.GraceMonthMinutes, "QUARTER": settings.GraceQuarterMinutes, "YEAR": settings.GraceYearMinutes}[r.PeriodKind]
	if !ok {
		return ErrInvalid
	}
	// Add in calendar minutes without duration overflow for large valid integers.
	if grace > int((int64(^uint64(0)>>1))/int64(time.Minute)) {
		return ErrInvalid
	}
	due := end.Add(time.Duration(grace) * time.Minute)
	r.DueAt = &due
	return nil
}

func (m *Module) reportRecipientOn(ctx context.Context, tx *gorm.DB, r *model.Report) (guid.ID, error) {
	author := reportAuthor(r)
	if author <= 0 {
		return 0, nil
	}
	p, human, err := m.principals.ActiveHumanByID(ctx, tx, author)
	if err != nil {
		return 0, err
	}
	if human && p.ID == author {
		return author, nil
	}
	if m.agents.ByPrincipalOn == nil {
		return 0, nil
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, r.TenantID, author)
	if err != nil {
		return 0, err
	}
	if found && a.PrincipalID == author && a.TenantID == r.TenantID && a.State == "ACTIVE" && a.OwnerPrincipalID > 0 {
		owner, active, err := m.principals.ActiveHumanByID(ctx, tx, a.OwnerPrincipalID)
		if err != nil {
			return 0, err
		}
		if active && owner.ID == a.OwnerPrincipalID {
			return owner.ID, nil
		}
	}
	return 0, nil
}

func (m *Module) scanOverdue(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if m.reportNotifier == nil || m.events == nil || m.audit == nil || m.principals.ActiveHumanByID == nil || m.principals.LockActiveRow == nil {
		return ErrUnavailable
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	var after guid.ID
	for {
		q := query.BindReports(db, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.DueAt.Lte(m.now()), q.OverdueNoticeCount.Lt(2), q.State.In("DRAFT", "READY", "BLOCKED", "RETURNED", "OVERDUE")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, candidate := range rows {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.overdueReportOn(ctx, tx, candidate) })
			if err != nil {
				return err
			}
			after = candidate.ID
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (m *Module) overdueReportOn(ctx context.Context, tx *gorm.DB, candidate *model.Report) error {
	recipient, err := m.reportRecipientOn(ctx, tx, candidate)
	if err != nil {
		return err
	}
	if recipient > 0 {
		principal, active, lockErr := m.principals.LockActiveRow(ctx, tx, recipient)
		if lockErr != nil {
			return lockErr
		}
		if !active || principal.ID != recipient {
			recipient = 0
		}
	}
	q := query.BindReports(tx, m.names)
	r, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(candidate.TenantID), q.ID.Eq(candidate.ID)).First()
	if err != nil {
		return err
	}
	if recipient > 0 {
		current, err := m.reportRecipientOn(ctx, tx, r)
		if err != nil {
			return err
		}
		// An owner change during the candidate read must never notify the old
		// owner. A later scan can acquire the current owner's principal lock.
		if current != recipient {
			return nil
		}
	}
	now := m.now()
	if r.DueAt == nil || !now.After(*r.DueAt) || r.OverdueNoticeCount >= 2 || !slices.Contains([]string{"DRAFT", "READY", "BLOCKED", "RETURNED", "OVERDUE"}, r.State) {
		return nil
	}
	if r.OverdueNoticeCount == 1 && now.Before(r.DueAt.Add(24*time.Hour)) {
		return nil
	}
	count := r.OverdueNoticeCount
	recipients := []guid.ID{}
	if recipient > 0 {
		recipients = append(recipients, recipient)
	}
	superior, err := m.reportConfirmerOn(ctx, tx, r)
	if err != nil {
		return err
	}
	if superior > 0 && superior != recipient {
		p, active, err := m.principals.LockActiveRow(ctx, tx, superior)
		if err != nil {
			return err
		}
		if active && p.ID == superior {
			current, err := m.reportConfirmerOn(ctx, tx, r)
			if err != nil {
				return err
			}
			if current == superior {
				recipients = append(recipients, superior)
			}
		}
	}
	if len(recipients) > 0 {
		path := fmt.Sprintf("/admin/%s/reports", r.TenantID)
		if r.ProjectID > 0 {
			path = fmt.Sprintf("/admin/%s/entities/%s/reports", r.TenantID, r.ProjectID)
		}
		ids, err := m.reportNotifier.Notify(ctx, tx, notification.Notification{TenantID: r.TenantID, ProjectID: r.ProjectID, Recipients: recipients, Type: "planning.report_overdue", Severity: notification.Action, TemplateKey: "planning.report_overdue", Permission: "report_read", LinkPath: path, DedupeKey: fmt.Sprintf("planning:overdue:%s:%d", r.ID, count), Params: map[string]string{"report_id": r.ID.String(), "period_kind": r.PeriodKind, "period_start": r.PeriodStart}})
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			count++
		}
	}
	if r.State == "OVERDUE" && count == r.OverdueNoticeCount {
		return nil
	}
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID), q.Version.Eq(r.Version)).Updates(map[string]any{"state": "OVERDUE", "overdue_notice_count": count, "version": r.Version + 1, "updated_at": now})
	if err != nil {
		return err
	}
	if info.RowsAffected != 1 {
		return ErrConflict
	}
	if err = m.audit(ctx, tx, audit.DomainEvent{TenantID: r.TenantID, EventType: "planning.report_overdue", TargetType: "planning_report", TargetID: r.ID, Detail: map[string]any{"authority_source": "scheduled:planning.report_overdue", "actor_kind": "SYSTEM", "due_at": r.DueAt, "notice_count": count}}); err != nil {
		return err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: "planning.report_overdue", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "scheduled:planning.report_overdue"}, Subject: events.Subject{Kind: "planning_report", ID: r.ID}, SubjectVersion: int64(r.Version + 1), Data: map[string]any{"due_at": r.DueAt, "notice_count": count}})
	return err
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
