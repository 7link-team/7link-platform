package collab

import (
	"context"
	"errors"

	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// RealtimeTopics delegates stream access to the same current membership and IAM
// checks as message reads. A thread ID is its root message, never a channel ID.
type RealtimeTopics struct {
	AuthorizeChannel func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
	AuthorizeThread  func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
}

func (m *Module) authorizeRealtimeChannel(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	return m.authorizeRealtime(ctx, tenant, principal, id, collection, false)
}

func (m *Module) authorizeRealtimeThread(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	return m.authorizeRealtime(ctx, tenant, principal, id, collection, true)
}

func (m *Module) authorizeRealtime(ctx context.Context, tenant, principal, id guid.ID, collection, thread bool) (bool, error) {
	if collection || tenant <= 0 || principal <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal {
		return false, nil
	}
	db, err := m.database()
	if err != nil {
		return false, err
	}
	allowed := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		channel := id
		internal := false
		if thread {
			q := query.BindMessages(tx, m.names)
			root, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ReplyToMessageID.IsNull(), q.WithdrawnAt.IsNull()).First()
			if e != nil {
				return e
			}
			channel, internal = root.ChannelID, root.Visibility == "INTERNAL"
		}
		q := query.BindChannels(tx, m.names)
		row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(channel), q.ArchivedAt.IsNull()).First()
		if e != nil {
			return e
		}
		if row.ProjectID == nil {
			return ErrNotFound
		}
		_, member, e := m.authorizeChannel(ctx, tx, tenant, *row.ProjectID, channel, "channel_read")
		if e != nil {
			return e
		}
		allowed = !internal || !member.External
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
		return false, nil
	}
	return allowed, err
}

func conversationTopics(tenant, channel guid.ID, thread *guid.ID) []string {
	topics := []string{"tenant/" + tenant.String() + "/channel/" + channel.String()}
	if thread != nil {
		topics = append(topics, "tenant/"+tenant.String()+"/thread/"+thread.String())
	}
	return topics
}

// Personal changes go only to the actor's inbox; other members must not learn
// who saved/read a message. Publication shares the command transaction.
func (m *Module) personalConversationEvent(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, event, actorKind string) error {
	if m.publisher == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if err := m.appendAudit(ctx, tx, tenant, project, channel, event, actor, ProvisioningAudit{AuthoritySource: "IAM:channel_read"}); err != nil {
		return err
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, Type: event, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: "IAM:channel_read"}, Subject: events.Subject{Kind: "inbox", ID: actor}, SubjectVersion: 1, Data: map[string]any{}})
	return err
}
