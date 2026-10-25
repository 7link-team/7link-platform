package planning

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindNotificationSource(s *module.Services) error {
	c, err := module.Resolve[notification.PlanningNotificationConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(notification.PlanningNotifications{CheckedOn: m.reportCheckedNotificationOn, LoopEligibleOn: m.loopNotificationEligibleOn, FactsOn: m.planningNotificationFactOn, EligibleOn: m.planningNotificationEligibleOn})
}

func (m *Module) planningNotificationFactOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) (notification.PlanningNotificationFact, bool, error) {
	var out notification.PlanningNotificationFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || (kind != "plan" && kind != "report" && kind != "goal") {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	var principal guid.ID
	scope := "MEMBER"
	if kind == "goal" {
		q := query.BindGoals(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.State, principal = r.ProjectID, r.State, r.PrincipalID
		_, end, err := m.periodForOn(ctx, tx, tenant, r.PeriodKind, r.PeriodStart)
		if err != nil {
			return out, false, err
		}
		due := end.AddDate(0, 0, 7)
		out.ReminderDueAt = &due
		settings, err := m.readSettingsOn(ctx, tx, tenant)
		if err != nil {
			return out, false, err
		}
		loc, err := time.LoadLocation(settings.Timezone)
		if err != nil {
			return out, false, err
		}
		out.DayKey = m.now().In(loc).Format("2006-01-02")
	} else if kind == "plan" {
		q := query.BindPlans(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.State, principal = r.ProjectID, r.State, r.PrincipalID
		if r.SubmittedAt != nil {
			due := r.SubmittedAt.Add(72 * time.Hour)
			out.ReminderDueAt = &due
		}
	} else {
		q := query.BindReports(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		out.ProjectID, out.State, principal = r.ProjectID, r.State, r.PrincipalID
		out.NeedsRecheck = r.NeedsRecheck
		out.ReminderDueAt = r.DueAt
		if principal == 0 {
			scope = "PROJECT"
			if out.ProjectID == 0 {
				scope = "TEAM"
			}
		}
	}
	root := "/admin/" + tenant.String()
	if out.ProjectID > 0 {
		root += "/entities/" + out.ProjectID.String()
	}
	out.LinkPath = root + "/" + kind + "s?record_id=" + id.String()
	if scope != "MEMBER" {
		out.LinkPath += "&scope=" + scope
	} else {
		out.LinkPath += "&principal_id=" + principal.String()
	}
	return out, true, nil
}

// Recipient context is used only for read/eligibility checks; no domain command
// executes as that recipient. Current principal/member/project fences are reused.
func (m *Module) planningNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, recipient guid.ID, kind, state string) (bool, error) {
	fact, found, err := m.planningNotificationFactOn(ctx, tx, tenant, id, kind)
	if err != nil || !found || (fact.State != state && !((state == "NEEDS_RECHECK" && fact.State == "SUBMITTED") || state == "OVERDUE_AUDIENCE")) {
		return false, err
	}
	var author, subject guid.ID
	if kind == "goal" {
		q := query.BindGoals(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return false, err
		}
		author, subject = r.PrincipalID, r.PrincipalID
	} else if kind == "plan" {
		q := query.BindPlans(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return false, err
		}
		author, subject = r.PrincipalID, r.PrincipalID
	} else {
		q := query.BindReports(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return false, err
		}
		subject = r.PrincipalID
		if state == "SUBMITTED" || state == "NEEDS_RECHECK" || state == "OVERDUE_AUDIENCE" {
			confirmer, err := m.reportConfirmerOn(ctx, tx, r)
			if err != nil {
				return false, err
			}
			author, err := m.reportRecipientOn(ctx, tx, r)
			if err != nil {
				return false, err
			}
			if state == "OVERDUE_AUDIENCE" {
				if r.DueAt == nil || !m.now().After(*r.DueAt) || !slices.Contains([]string{"DRAFT", "READY", "BLOCKED", "RETURNED", "OVERDUE"}, r.State) || (recipient != author && recipient != confirmer) {
					return false, nil
				}
				readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
				_, err = m.authorize(readCtx, tx, tenant, r.ProjectID, r.PrincipalID, "report_read", true)
				if errors.Is(err, ErrForbidden) {
					return false, nil
				}
				return err == nil, err
			}
			if state == "NEEDS_RECHECK" {
				if !r.NeedsRecheck || r.State != "SUBMITTED" || (recipient != author && recipient != confirmer) {
					return false, nil
				}
				readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
				_, err = m.authorize(readCtx, tx, tenant, r.ProjectID, r.PrincipalID, "report_read", true)
				if errors.Is(err, ErrForbidden) {
					return false, nil
				}
				return err == nil, err
			}
			if recipient != confirmer {
				return false, nil
			}
		}
		author, err = m.reportRecipientOn(ctx, tx, r)
		if err != nil {
			return false, err
		}
	}
	if state == "SUBMITTED" {
		if author == recipient {
			return false, nil
		}
	} else if author != recipient {
		if kind != "goal" || state != "AT_RISK" {
			return false, nil
		}
		q := query.BindGoals(tx, m.names)
		g, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return false, err
		}
		var body PlanningGoalInput
		if json.Unmarshal([]byte(g.Content), &body) != nil {
			return false, ErrInvalid
		}
		if body.ParentGoalID == nil {
			return false, nil
		}
		parent, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*body.ParentGoalID)).First()
		if err != nil {
			return false, err
		}
		if parent.PrincipalID != recipient || recipient <= 0 {
			return false, nil
		}
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
	permissions := []string{"work_item_read"}
	if kind == "report" {
		permissions = []string{"report_read"}
	}
	if kind == "goal" {
		permissions = []string{"goal_read"}
	}
	if state == "SUBMITTED" {
		permissions = append(permissions, kind+"_confirm")
	}
	for _, permission := range permissions {
		if _, err = m.authorize(readCtx, tx, tenant, fact.ProjectID, subject, permission, true); errors.Is(err, ErrForbidden) {
			return false, nil
		} else if err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *Module) loopNotificationEligibleOn(ctx context.Context, tx *gorm.DB, tenant, id, recipient guid.ID, typ string) (bool, error) {
	q := query.BindLoopRuns(tx, m.names)
	run, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.State != "ESCALATED" && run.State != "TIMED_OUT" {
		return false, nil
	}
	c, err := m.loopOn(ctx, tx, tenant, run.LoopContractID)
	if err != nil {
		return false, err
	}
	yes := recipient == c.OwnerPrincipalID
	if typ == "planning.loop_paused" {
		if c.State != "PAUSED" {
			return false, nil
		}
		if m.agents.ByPrincipalOn == nil {
			return false, ErrUnavailable
		}
		agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, c.AgentID)
		if err != nil {
			return false, err
		}
		yes = yes || (found && recipient == agent.OwnerPrincipalID)
	}
	return yes, nil
}

func (m *Module) recordReportCheckOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (PlanningCheck, error) {
	ctx = core.WithEntityMutation(ctx)
	r, actorKind, err := m.reportOn(ctx, tx, tenant, id, "report_read", false)
	if err != nil {
		return PlanningCheck{}, err
	}
	check, err := m.checkReport(ctx, tx, tenant, r)
	if err != nil {
		return check, err
	}
	if m.events == nil || m.audit == nil {
		return check, ErrUnavailable
	}
	if authn.PrincipalIDFromContext(ctx) == reportAuthor(r) {
		if _, err = m.authorize(ctx, tx, tenant, r.ProjectID, r.PrincipalID, "report_submit", false); err != nil {
			return check, err
		}
		hash := ""
		if check.Result != "BLOCKED" {
			hash = r.ContentHash
		}
		facts, _, err := hashJSON(check.Facts)
		if err != nil {
			return check, err
		}
		_, err = query.BindReports(tx, m.names).WithContext(ctx).Where(query.BindReports(tx, m.names).TenantID.Eq(tenant), query.BindReports(tx, m.names).ID.Eq(id)).Updates(map[string]any{"facts": facts, "last_check_hash": hash, "needs_recheck": check.Result == "BLOCKED" && r.NeedsRecheck})
		if err != nil {
			return check, err
		}
	}
	checkID, err := m.ids.Next()
	if err != nil {
		return check, err
	}
	if checkID <= 0 {
		return check, ErrUnavailable
	}
	if err = query.BindReportChecks(tx, m.names).WithContext(ctx).Create(&model.ReportCheck{TenantID: tenant, ID: guid.ID(checkID), ReportID: r.ID, ReportVersion: r.Version, Result: check.Result, CheckedAt: m.now()}); err != nil {
		return check, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err = m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "planning.report_checked", TargetType: "planning_report", TargetID: id, Detail: map[string]any{"actor_id": actor.String(), "result": check.Result, "report_version": r.Version, "authority_source": "IAM:report_read"}}); err != nil {
		return check, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: r.ProjectID, Type: "planning.report_checked", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: "IAM:report_read"}, Subject: events.Subject{Kind: "planning_report", ID: id}, SubjectVersion: int64(r.Version), Data: map[string]any{"result": check.Result, "report_version": r.Version}})
	return check, err
}
func (m *Module) reportCheckedNotificationOn(ctx context.Context, tx *gorm.DB, tenant, id, actor guid.ID, version int) (bool, error) {
	q := query.BindReports(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if r.State == "CONFIRMED" || r.State == "SUBMITTED" {
		return false, nil
	}
	recipient, err := m.reportRecipientOn(ctx, tx, r)
	if err != nil || recipient != actor {
		return false, err
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: actor})
	if _, _, err = m.reportOn(readCtx, tx, tenant, id, "report_read", false); errors.Is(err, ErrForbidden) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	checks := query.BindReportChecks(tx, m.names)
	receipt, err := checks.WithContext(ctx).Where(checks.TenantID.Eq(tenant), checks.ReportID.Eq(id), checks.ReportVersion.Eq(version)).Order(checks.CheckedAt.Desc(), checks.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if receipt.Result != "BLOCKED" {
		return false, nil
	}
	check, err := m.checkReport(readCtx, tx, tenant, r)
	return check.Result == "BLOCKED", err
}
