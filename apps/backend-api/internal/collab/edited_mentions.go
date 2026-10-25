package collab

import (
	"context"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// EditedMentionNotifications resolves only newly added mentions from an edit
// event, intersected with current channel visibility. Notification projection
// must subscribe to collab.message_edited and pass added_mention_ids here.
type EditedMentionNotifications struct {
	RecipientsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]guid.ID, error)
}

func (m *Module) provideEditedMentions(s *module.Services) error {
	return module.Provide[EditedMentionNotifications](s, EditedMentionNotifications{RecipientsOn: m.editedMentionRecipientsOn})
}
func (m *Module) editedMentionRecipientsOn(ctx context.Context, tx *gorm.DB, tenant, message guid.ID, added []guid.ID) ([]guid.ID, error) {
	current, err := m.notificationMentionsOn(ctx, tx, tenant, message)
	if err != nil {
		return nil, err
	}
	out := []guid.ID{}
	for _, id := range current {
		if slices.Contains(added, id) {
			out = append(out, id)
		}
	}
	return out, nil
}
