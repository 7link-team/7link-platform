package notification

import (
	activityi18n "7link.com/platform/internal/notification/i18n"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) injectAcceptance(s *module.Services) error {
	facts, e := module.Resolve[clientwork.AcceptanceReminderFacts](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	m.acceptanceReminderFacts = facts
	catalog, e := module.Resolve[events.SubscriberCatalog](s)
	if e != nil {
		return e
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_notification_acceptance_reminders_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_notification_acceptance_reminders_description", Name: "notification.acceptance_reminders", EventTypes: []string{"clientwork.acceptance_reminder", "clientwork.acceptance_overdue"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.acceptanceOn(eventCtx, tx, e, facts); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}})
}

func (m *Module) acceptanceOn(ctx context.Context, tx *gorm.DB, e events.Envelope, facts clientwork.AcceptanceReminderFacts) error {
	if facts == nil || m.principal.LockActive == nil {
		return ErrUnavailable
	}
	var discovery struct {
		RecipientID guid.ID `json:"recipient_id"`
	}
	if json.Unmarshal(e.Data, &discovery) != nil || discovery.RecipientID <= 0 {
		return ErrInvalid
	}
	// The envelope supplies a lock candidate only. Current source facts below
	// determine the recipient and must match before any notification is written.
	ids := []guid.ID{discovery.RecipientID}
	if e.EventType == "clientwork.acceptance_overdue" {
		if m.candidates == nil {
			return ErrUnavailable
		}
		ids = []guid.ID{}
		var after guid.ID
		for {
			page, err := m.candidates(ctx, tx, after, 100)
			if err != nil {
				return err
			}
			for _, id := range page {
				if id <= after {
					return ErrInvalid
				}
				after = id
				ids = append(ids, id)
			}
			if len(page) < 100 {
				break
			}
		}
	}
	for _, id := range ids {
		if err := m.principal.LockActive(ctx, tx, id); err != nil {
			return err
		}
	}
	f, found, err := facts(ctx, tx, e.TenantID, e.Subject.ID)
	if err != nil || !found {
		return err
	}
	if f.ProjectID != e.ProjectID || f.RecipientID != discovery.RecipientID || int64(f.Day) != e.SubjectVersion || f.Escalation != (e.EventType == "clientwork.acceptance_overdue") {
		return ErrInvalid
	}
	for _, id := range ids {
		typ, template, permission := "delivery.acceptance_reminder", "clientwork.acceptance_reminder", "delivery_read"
		if f.Escalation {
			lead, err := m.leadOn(ctx, tx, e.TenantID, e.ProjectID, id)
			if err != nil {
				return err
			}
			if !lead {
				continue
			}
			typ, template, permission = "delivery.acceptance_overdue", "clientwork.acceptance_overdue", "project_read"
		}
		if err := m.notifySourceOn(ctx, tx, Notification{Channels: []ChannelName{}, TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: Action, Permission: permission, TemplateKey: template, Params: map[string]string{"subject_id": e.Subject.ID.String(), "package_id": f.PackageID.String(), "due_at": f.DueAt.UTC().Format(time.RFC3339)}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/deliveries/" + f.PackageID.String() + "?tab=timeline", DedupeKey: typ + ":" + e.Subject.ID.String() + ":" + strconv.Itoa(f.Day)}, e.EventID, e.Subject.ID); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) acceptanceAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.acceptanceReminderFacts == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, e := guid.RequireID(params["subject_id"])
	if e != nil {
		return false, ErrInvalid
	}
	f, found, e := m.acceptanceReminderFacts(ctx, tx, row.TenantID, id)
	if e != nil || !found || f.ProjectID != row.ProjectID || f.PackageID.String() != params["package_id"] {
		return false, e
	}
	if row.Type == "delivery.acceptance_reminder" {
		return !f.Escalation && principal == f.RecipientID, nil
	}
	if !f.Escalation {
		return false, nil
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
}
