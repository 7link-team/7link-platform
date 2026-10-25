package events

import (
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

type ParkedNotice struct {
	Subscriber        string
	Count             int64
	EventID, TenantID guid.ID
}

// ParkedNotifications binds the notification owner without an infrastructure-to-
// business dependency. Delivery participates in the new FAILED row transaction.
type ParkedNotifications struct {
	Bind func(func(context.Context, *gorm.DB, ParkedNotice) error) error
}

func (m *Module) provideParkedNotifications(s *module.Services) error {
	return module.Provide[ParkedNotifications](s, ParkedNotifications{Bind: func(fn func(context.Context, *gorm.DB, ParkedNotice) error) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if fn == nil || m.sealed || m.onParked != nil {
			return ErrInvalidEvent
		}
		m.onParked = fn
		return nil
	}})
}
func (m *Module) notifyParked(ctx context.Context, tx *gorm.DB, name string, e Envelope, id guid.ID) error {
	if m.onParked == nil {
		return nil
	}
	q := query.BindParked(tx, m.names)
	inserted, err := q.WithContext(ctx).Where(q.ID.Eq(id)).Count()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return nil
	}
	count, err := q.WithContext(ctx).Where(q.Consumer.Eq(name), q.Reason.Eq("FAILED"), q.State.In("PARKED", "REDELIVERING")).Count()
	if err != nil {
		return err
	}
	return m.onParked(ctx, tx, ParkedNotice{Subscriber: name, Count: count, EventID: e.EventID, TenantID: e.TenantID})
}
