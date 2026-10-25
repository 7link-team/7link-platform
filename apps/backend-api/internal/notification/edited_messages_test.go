package notification

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEditedMentionSubscriberRejectsInvalidAndUnavailableCurrentMentions(t *testing.T) {
	for _, mode := range []string{"identity", "payload", "missing", "source", "no_current", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			e := events.Envelope{EventID: 1001, EventType: "collab.message_edited", TenantID: 7, ProjectID: 81, Subject: events.Subject{ID: 91, Kind: "message"}, Data: []byte(`{"channel_id":"80","added_mention_ids":["11"]}`)}
			f.m.mentions.RecipientsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) {
				if mode == "source" {
					return nil, ErrUnavailable
				}
				return nil, nil
			}
			switch mode {
			case "identity":
				e.Subject.ID = 0
			case "payload":
				e.Data = []byte(`{}`)
			case "missing":
				f.m.mentions.RecipientsOn = nil
			case "empty":
				e.Data = []byte(`{"channel_id":"80"}`)
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.messageEditedSubscriber().Handle(t.Context(), tx, e) })
			switch mode {
			case "no_current", "empty":
				require.ErrorIs(t, err, events.ErrInvalidEvent)
			case "identity", "payload":
				require.ErrorIs(t, err, ErrInvalid)
			default:
				require.ErrorIs(t, err, ErrUnavailable)
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestEditedMentionsNotifyOnlyNewCurrentlyVisibleRecipients(t *testing.T) {
	f := newFixture(t)
	f.m.mentions = collab.MentionNotifications{RecipientsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11, 12}, nil }, VisibleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }}
	event := events.Envelope{EventType: "collab.message_edited", TenantID: 7, ProjectID: 3, Subject: events.Subject{Kind: "message", ID: 81}, Actor: events.Actor{ID: 12}, Data: json.RawMessage(`{"channel_id":"61","added_mention_ids":["11","13"]}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectMessageEditedOn(t.Context(), tx, event) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID)
	event.Data = json.RawMessage(`{"channel_id":"61","added_mention_ids":[]}`)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectMessageEditedOn(t.Context(), tx, event) }))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Equal(t, []string{"collab.message_edited"}, f.m.messageEditedSubscriber().EventTypes)
	require.NoError(t, events.NewModule(f.m.runtime).AddSubscribers(Name, f.m.messageEditedSubscriber()), "production event catalog must accept the subscription")
}
