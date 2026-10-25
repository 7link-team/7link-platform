package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
	"time"
)

func (m *Module) scanNotificationReminders(ctx context.Context, tenant guid.ID) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	for _, kind := range []string{"plan", "goal"} {
		var after guid.ID
		for {
			var ids []guid.ID
			if kind == "plan" {
				q := query.BindPlans(db, m.names)
				rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.State.Eq("SUBMITTED")).Order(q.ID).Limit(100).Find()
				if err != nil {
					return err
				}
				for _, row := range rows {
					ids = append(ids, row.ID)
				}
			} else {
				q := query.BindGoals(db, m.names)
				rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.State.In("ACTIVE", "AT_RISK")).Order(q.ID).Limit(100).Find()
				if err != nil {
					return err
				}
				for _, row := range rows {
					ids = append(ids, row.ID)
				}
			}
			for _, id := range ids {
				after = id
				if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.notificationReminderOn(ctx, tx, tenant, id, kind) }); err != nil {
					return err
				}
			}
			if len(ids) < 100 {
				break
			}
		}
	}
	return nil
}

func (m *Module) notificationReminderOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string) error {
	if authn.PrincipalIDFromContext(ctx) != 0 {
		return ErrForbidden
	}
	fact, found, err := m.planningNotificationFactOn(ctx, tx, tenant, id, kind)
	if err != nil || !found || fact.ReminderDueAt == nil || m.now().Before(*fact.ReminderDueAt) {
		return err
	}
	if kind == "plan" && fact.State != "SUBMITTED" {
		return nil
	}
	if kind == "goal" && fact.State != "ACTIVE" && fact.State != "AT_RISK" {
		return nil
	}
	if m.notificationCandidates == nil || m.reportNotifier == nil {
		return ErrUnavailable
	}
	typ, permission := "planning.plan_unconfirmed", "work_item_read"
	if kind == "goal" {
		typ, permission = "planning.goal_close_overdue", "goal_read"
	}
	dedupeKey := "planning-reminder:" + kind + ":" + id.String()
	if kind == "plan" {
		dedupeKey += ":" + fact.ReminderDueAt.UTC().Format(time.RFC3339Nano)
	}
	var after guid.ID
	for {
		candidates, err := m.notificationCandidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, recipient := range candidates {
			if recipient <= after {
				return ErrInvalid
			}
			after = recipient
			eligible, err := m.planningNotificationEligibleOn(ctx, tx, tenant, id, recipient, kind, fact.State)
			if err != nil {
				return err
			}
			if !eligible {
				continue
			}
			_, err = m.reportNotifier.Notify(ctx, tx, notification.Notification{TenantID: tenant, ProjectID: fact.ProjectID, Recipients: []guid.ID{recipient}, Type: typ, Severity: notification.Info, TemplateKey: "notification." + map[string]string{"plan": "planning_plan_unconfirmed", "goal": "planning_goal_close_overdue"}[kind], Permission: permission, Params: map[string]string{"subject_id": id.String()}, LinkPath: fact.LinkPath, DedupeKey: dedupeKey})
			if err != nil {
				return err
			}
		}
		if len(candidates) < 100 {
			if kind == "plan" {
				return m.markPlanUnconfirmedOn(ctx, tx, tenant, id)
			}
			return nil
		}
	}
}

// Keep confirmation actionable after timeout. Notify and the state/audit/outbox
// transition commit together; retries retain the submission-specific dedupe key.
func (m *Module) markPlanUnconfirmedOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
	q := query.BindPlans(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if row.State != "SUBMITTED" || row.UnconfirmedAt != nil || row.SubmittedAt == nil || m.now().Before(row.SubmittedAt.Add(72*time.Hour)) {
		return nil
	}
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(row.Version), q.State.Eq("SUBMITTED")).Updates(map[string]any{"unconfirmed_at": m.now(), "version": row.Version + 1, "updated_at": m.now()})
	if err != nil {
		return err
	}
	if info.RowsAffected != 1 {
		return ErrConflict
	}
	at := m.now()
	row.UnconfirmedAt = &at
	row.Version++
	return m.record(ctx, tx, tenant, planView(row), "SYSTEM", "UNCONFIRMED", "")
}
