package notification

import (
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	activityi18n "7link.com/platform/internal/notification/i18n"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strconv"
	"time"
)

// injectReminder registers a separate transactional consumer without
// altering other notification projections. It sends a generic reminder
// only to the current real client; the linked portal rechecks its own access.
func (m *Module) injectReminder(s *module.Services) error {
	facts, err := module.Resolve[clientwork.ClarificationReminderFacts](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	if err = catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_notification_clarification_reminder_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_notification_clarification_reminder_description", Name: "notification.clarification_reminder", EventTypes: []string{"clientwork.clarification_reminder"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.reminderOn(eventCtx, tx, e, facts); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}}); err != nil {
		return err
	}
	return m.injectStarvation(s, catalog)
}
func (m *Module) reminderOn(ctx context.Context, tx *gorm.DB, e events.Envelope, facts clientwork.ClarificationReminderFacts) error {
	if facts == nil {
		return ErrUnavailable
	}
	fact, found, err := facts(ctx, tx, e.TenantID, e.Subject.ID)
	if err != nil || !found {
		return err
	}
	if fact.ProjectID != e.ProjectID || fact.RecipientID <= 0 || fact.Ordinal < 1 || e.SubjectVersion < 1 || e.SubjectVersion > int64(fact.Ordinal) {
		return ErrInvalid
	}
	err = m.notifySourceOn(ctx, tx, Notification{Channels: []ChannelName{}, TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{fact.RecipientID}, Type: "intake.clarification_reminder", Severity: Action, TemplateKey: "clientwork.clarification_reminder", Params: map[string]string{"subject_id": e.Subject.ID.String(), "due_at": fact.DueAt.UTC().Format(time.RFC3339)}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/baseline?clarification=" + e.Subject.ID.String(), DedupeKey: fmt.Sprintf("clarification:%s:reminder:%s", e.Subject.ID, strconv.Itoa(int(e.SubjectVersion)))}, e.EventID, e.Subject.ID)
	if err != nil {
		return err
	}
	return nil
}

func (m *Module) injectStarvation(s *module.Services, catalog events.SubscriberCatalog) error {
	facts, err := module.Resolve[delivery.DispatchStarvationFacts](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_notification_dispatch_starvation_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_notification_dispatch_starvation_description", Name: "notification.dispatch_starvation", EventTypes: []string{"clientwork.dispatch_starved"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.starvationOn(eventCtx, tx, e, facts); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}})
}
func (m *Module) starvationOn(ctx context.Context, tx *gorm.DB, e events.Envelope, facts delivery.DispatchStarvationFacts) error {
	if facts.CurrentOn == nil || m.candidates == nil || m.guard == nil {
		return ErrUnavailable
	}
	eligible, err := facts.CurrentOn(ctx, tx, e.TenantID, e.ProjectID, e.Subject.ID, m.now())
	if err != nil || !eligible {
		return err
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			yes, err := m.leadOn(ctx, tx, e.TenantID, e.ProjectID, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if err = m.principal.LockActive(ctx, tx, id); err != nil {
				return err
			}
			key := "dispatch-starved:" + e.Subject.ID.String()
			if err = m.guard(ctx, tx, key+":"+id.String()); err != nil {
				return err
			}
			q := query.BindNotifications(tx, m.names)
			count, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProjectID.Eq(e.ProjectID), q.RecipientPrincipalID.Eq(id), q.Type.Eq("dispatch.starved"), q.DedupeKey.Eq(key), q.CreatedAt.Gt(m.now().Add(-4*time.Hour))).Count()
			if err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{Channels: []ChannelName{}, TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: "dispatch.starved", Severity: Action, TemplateKey: "clientwork.dispatch_starved", Permission: "project_read", Params: map[string]string{"subject_id": e.Subject.ID.String()}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/dispatch", DedupeKey: key})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
