package realtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"7link.com/platform/internal/infra/events"
	"gorm.io/gorm"
)

// Message selects the SSE event type and its public payload.
type Message struct {
	Type    string
	Payload any
}

type broadcast struct {
	eventType string
	topic     string
	parsed    Topic
	data      json.RawMessage
}

// Broadcast coalesces a topic's most recent payload over a 100 ms window.
// It is a realtime hint, not a replacement for a durable domain command.
func (m *Module) Broadcast(ctx context.Context, topic string, data any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t, _, err := parseTopic(topic)
	if err != nil {
		return err
	}
	eventType := "realtime.broadcast"
	if message, ok := data.(Message); ok {
		eventType = message.Type
		data = message.Payload
	}
	if len(eventType) > 96 || !eventTypeRE.MatchString(eventType) {
		return ErrInvalid
	}
	encoded, err := json.Marshal(map[string]any{"payload": data})
	if err != nil || len(encoded) > 4096 {
		return ErrInvalid
	}
	m.hub.mu.Lock()
	active := m.hub.cancel != nil && !m.hub.closing
	m.hub.mu.Unlock()
	if !active {
		return ErrUnavailable
	}
	m.mu.Lock()
	m.pending[topic] = broadcast{topic: topic, parsed: t, data: encoded, eventType: eventType}
	m.mu.Unlock()
	return nil
}
func (m *Module) flushBroadcasts(ctx context.Context) {
	m.mu.Lock()
	pending := m.pending
	m.pending = map[string]broadcast{}
	m.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		slog.ErrorContext(ctx, "broadcast realtime events", "error", ErrUnavailable)
		return
	}
	db := m.runtime.Database().Write()
	for _, b := range pending {
		id := b.parsed.ID
		if id == 0 {
			id = b.parsed.TenantID
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			_, err := m.publisher(ctx, tx, events.Event{TenantID: b.parsed.TenantID, Type: b.eventType, Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "realtime", ID: id}, ExtraTopics: []string{b.topic}, Data: b.data})
			return err
		})
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "publish realtime broadcast", "error", err)
		}
	}
}
