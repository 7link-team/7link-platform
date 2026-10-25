package events

import (
	"7link.com/platform/internal/infra/events/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"time"
)

// RecordedEvent is a provider-owned immutable outbox view, without storage types.
type RecordedEvent struct {
	ID, TenantID, ProjectID, SubjectID, ActorID guid.ID
	Type, SubjectKind                           string
	Data                                        json.RawMessage
	OccurredAt                                  time.Time
}

// EventReader pages committed rows in the caller snapshot. Consumers must retain
// receipts and rescan: snowflake IDs do not imply database commit order.
type EventReader func(context.Context, *gorm.DB, guid.ID, int) ([]RecordedEvent, error)

func (m *Module) ReadEventsOn(ctx context.Context, tx *gorm.DB, after guid.ID, limit int) ([]RecordedEvent, error) {
	if m == nil || tx == nil || after < 0 || limit < 1 || limit > 200 {
		return nil, ErrInvalidEvent
	}
	q := query.BindWithNames(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.ID.Gt(after), q.Durable.Is(true)).Order(q.ID).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	out := make([]RecordedEvent, 0, len(rows))
	for _, r := range rows {
		v := RecordedEvent{ID: r.ID, TenantID: r.TenantID, SubjectID: r.SubjectID, Type: r.EventType, SubjectKind: r.SubjectKind, Data: json.RawMessage(r.Data), OccurredAt: r.OccurredAt}
		if r.ProjectID != nil {
			v.ProjectID = *r.ProjectID
		}
		if r.ActorID != nil {
			v.ActorID = *r.ActorID
		}
		out = append(out, v)
	}
	return out, nil
}
