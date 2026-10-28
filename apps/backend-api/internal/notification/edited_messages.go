package notification

import (
	activityi18n "7link.com/platform/internal/notification/i18n"
	"context"
	"encoding/json"
	"slices"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// Edits have a separate transactional subscription so the existing posted
// projector cannot notify every previously mentioned participant again.
func (m *Module) messageEditedSubscriber() events.Subscriber {
	return events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_notification_message_edits_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_notification_message_edits_description", Name: Name + ".message_edits", EventTypes: []string{"collab.message_edited"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: func(ctx context.Context, tx *gorm.DB, envelope events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.projectMessageEditedOn(eventCtx, tx, envelope); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}}
}
func (m *Module) projectMessageEditedOn(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if !validTransaction(tx) || e.EventType != "collab.message_edited" || e.Subject.Kind != "message" || e.Subject.ID <= 0 || e.TenantID <= 0 {
		return ErrInvalid
	}
	var data struct {
		ChannelID guid.ID   `json:"channel_id"`
		Added     []guid.ID `json:"added_mention_ids"`
	}
	if json.Unmarshal(e.Data, &data) != nil || data.ChannelID <= 0 || len(data.Added) > 50 {
		return ErrInvalid
	}
	if len(data.Added) == 0 {
		return nil
	}
	if m.mentions.RecipientsOn == nil {
		return ErrUnavailable
	}
	current, err := m.mentions.RecipientsOn(ctx, tx, e.TenantID, e.Subject.ID)
	if err != nil {
		return err
	}
	recipients := []guid.ID{}
	for _, id := range current {
		if id != e.Actor.ID && slices.Contains(data.Added, id) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return nil
	}
	_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: recipients, Type: "channel.mentioned", Severity: Info, TemplateKey: "notification.mentioned", Params: map[string]string{"subject_id": e.Subject.ID.String(), "type": "channel.mentioned"}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/conversations?channel_id=" + data.ChannelID.String(), DedupeKey: "mention:" + data.ChannelID.String(), Permission: "channel_read"})
	return err
}
