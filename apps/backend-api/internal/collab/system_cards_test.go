package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestSystemCardStorageFailuresRollBackMessageAndChannelSequence(t *testing.T) {
	for _, scenario := range []struct{ operation, table string }{
		{"query", "c_collab_system_cards"}, {"create", "c_collab_messages"}, {"create", "c_collab_system_cards"}, {"update", "c_collab_channels"}, {"create", "c_domain_events"},
	} {
		t.Run(scenario.operation+scenario.table, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			fault := errors.New("card storage failure")
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == scenario.table {
					tx.AddError(fault)
				}
			}
			remove := func() {}
			switch scenario.operation {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:card", inject))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:card")) }
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:card", inject))
				remove = func() { require.NoError(t, db.Callback().Create().Remove("test:card")) }
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:card", inject))
				remove = func() { require.NoError(t, db.Callback().Update().Remove("test:card")) }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.postSystemCardOn(t.Context(), tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 91, Kind: "EVENT", Body: "actual fact"})
				return err
			})
			remove()
			require.ErrorIs(t, err, fault)
			var n int64
			for _, table := range []string{m.names.Messages.String(), "c_collab_system_cards"} {
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
			var stored model.Channel
			require.NoError(t, db.Table(m.names.Channels.String()).Where("id = ?", ch.ID).Take(&stored).Error)
			require.Zero(t, stored.LastSeq)
		})
	}
}

func TestSystemCardRejectsInvalidPayloadAndUnresolvableDispatchFacts(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	for _, in := range []SystemCardInput{
		{SourceID: 0, Kind: "EVENT", Body: "fact"},
		{SourceID: 91, Kind: "EVENT", Body: "fact", Payload: map[string]any{"unsupported": make(chan int)}},
		{SourceID: 91, Kind: "EVENT", Body: "fact", Payload: map[string]any{"large": strings.Repeat("x", 32001)}},
	} {
		in.TenantID, in.ProjectID, in.ChannelID = 1, 3, ch.ID
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.postSystemCardOn(t.Context(), tx, in); return err }), ErrInvalidArgument)
	}
	event := events.Envelope{TenantID: 1, ProjectID: 3, EventID: 99, EventType: "clientwork.dispatch_proposed", Subject: events.Subject{ID: 91}, Data: []byte(`{}`)}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.projectSystemEventOn(t.Context(), tx, event) }), ErrUnavailable)
	fault := errors.New("dispatch unavailable")
	m.channelDispatchFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (map[string]any, error) { return nil, fault }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.projectSystemEventOn(t.Context(), tx, event) }), fault)
	m.channelDispatchFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (map[string]any, error) {
		return nil, gorm.ErrRecordNotFound
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.projectSystemEventOn(t.Context(), tx, event) }))
	event.Data = []byte(`invalid`)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.projectSystemEventOn(t.Context(), tx, event) }))
	var n int64
	require.NoError(t, db.Table(m.names.Messages.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestDispatchCardRefreshFailurePreservesPreviousCommittedFacts(t *testing.T) {
	for _, scenario := range []string{"corrupt_payload", "update", "audit", "outbox", "missing_message"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			in := SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 91, Kind: "DISPATCH_CARD", Body: "proposal", Payload: map[string]any{"state": "PROPOSED"}}
			var first MessageView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				first, err = m.postSystemCardOn(t.Context(), tx, in)
				return err
			}))
			fault := errors.New("refresh dependency failure")
			remove := func() {}
			switch scenario {
			case "corrupt_payload":
				require.NoError(t, query.BindSystemCards(db, m.names).Where(&model.SystemCard{MessageID: first.ID}).Update("payload", "bad").Error)
			case "missing_message":
				require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", first.ID).Delete(&model.Message{}).Error)
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:refresh", func(tx *gorm.DB) {
					if tx.Statement.Table == "c_collab_system_cards" {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Update().Remove("test:refresh")) }
			case "outbox":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:refresh", func(tx *gorm.DB) {
					if tx.Statement.Table == "c_domain_events" {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Create().Remove("test:refresh")) }
			}
			in.Payload = map[string]any{"state": "APPROVED"}
			err := db.Transaction(func(tx *gorm.DB) error { _, err := m.postSystemCardOn(t.Context(), tx, in); return err })
			remove()
			require.Error(t, err)
			if scenario == "audit" || scenario == "update" || scenario == "outbox" {
				require.ErrorIs(t, err, fault)
			}
			var stored model.SystemCard
			require.NoError(t, query.BindSystemCards(db, m.names).Where(&model.SystemCard{MessageID: first.ID}).First(&stored).Error)
			require.NotContains(t, stored.Payload, "APPROVED")
		})
	}
}

func TestSystemCardsAreIdempotentImmutableAndCurrentVisibilityFiltered(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	ctx := conversationContext(t, 2)
	var card MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		card, e = m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 91, Kind: "DECISION_CARD", Body: "Review required", Payload: map[string]any{"work_item_id": "99", "status": "NEEDS_HUMAN_REVIEW"}})
		return e
	}))
	require.Equal(t, "DECISION_CARD", card.Kind)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, e := m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 91, Kind: "DECISION_CARD", Body: "Review required"})
		require.Equal(t, card.ID, replay.ID)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mutateMessageOn(ctx, tx, 1, card.ID, 1, EditMessageInput{Body: "edited"}, false)
		return e
	}), ErrMessageNotEditable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listMessagesOn(ctx, tx, 1, 3, ch.ID, MessageFilter{Limit: 50, Activity: "all"})
		require.Len(t, page.Items, 1)
		require.Equal(t, "DECISION_CARD", page.Items[0].Kind)
		return e
	}))
	_, e := m.postSystemCardOn(context.Background(), db, SystemCardInput{})
	require.Error(t, e)
}
func TestSystemProjectorUsesOnlyCommittedEventFactsAndProtectsAttention(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	e := db.Transaction(func(tx *gorm.DB) error {
		return m.projectSystemEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, EventID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: 8}, Data: []byte(`{"to":"NEEDS_HUMAN_REVIEW","secret":"not projected"}`)})
	})
	// This custom-only project has no default channel, so projection is an honest
	// no-op rather than creating a channel or fabricating an external audience.
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 50, Activity: "all"})
		require.Empty(t, page.Items)
		return e
	}))
}

func TestSystemProjectorPersistsWhitelistedDecisionAndCurrentDispatchFacts(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	q := query.BindChannels(db, m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(ch.ID)).Update(q.Kind, "PROJECT_DEFAULT")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.projectSystemEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, EventID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: 8}, Data: []byte(`{"to":"NEEDS_HUMAN_REVIEW","secret":"private input"}`)})
	}))
	var cards []model.SystemCard
	require.NoError(t, query.BindSystemCards(db, m.names).Find(&cards).Error)
	require.Len(t, cards, 1)
	require.Equal(t, "DECISION_CARD", cards[0].Kind)
	require.NotContains(t, cards[0].Payload, "private input")

	for index, eventType := range []string{"execution.state_changed", "execution.verification_completed", "budget.threshold_reached"} {
		data := []byte(`{"state":"FAILED"}`)
		if index == 1 {
			data = []byte(`{"verdict":"FAILED"}`)
		}
		if index == 2 {
			data = []byte(`{"threshold_pct":80}`)
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.projectSystemEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, EventID: guid.ID(201 + index), EventType: eventType, Subject: events.Subject{Kind: "execution", ID: 8}, Data: data})
		}))
		var attention model.SystemCard
		require.NoError(t, query.BindSystemCards(db, m.names).Where(&model.SystemCard{SourceID: guid.ID(201 + index)}).First(&attention).Error)
		require.True(t, attention.Attention)
	}
	// The requested channel is distinct from the default channel. Both copies must
	// follow the actual source state, including delayed/replayed proposed events.
	custom := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postSystemCardOn(t.Context(), tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: custom.ID, SourceID: 71, Kind: "DISPATCH_CARD", Body: "CHANNEL_REQUEST", Payload: map[string]any{"status": "PROPOSED", "source_message_id": "52"}})
		return e
	}))
	m.channelDispatchFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (map[string]any, error) {
		return map[string]any{"status": "DISMISSED", "result": "HUMAN_DISMISSED", "proposal_id": "71", "work_item_id": "8"}, nil
	}
	for _, eventType := range []string{"clientwork.dispatch_decided", "clientwork.dispatch_proposed"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.projectSystemEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, EventID: 100, EventType: eventType, Subject: events.Subject{Kind: "clientwork", ID: 71}, Data: []byte(`{"version":2}`)})
		}))
	}
	cards = nil
	require.NoError(t, query.BindSystemCards(db, m.names).Where(&model.SystemCard{SourceID: 71}).Find(&cards).Error)
	require.Len(t, cards, 2)
	for _, card := range cards {
		var view MessageView
		applySystemCard(&view, card)
		require.Equal(t, "DISMISSED", view.Payload["status"])
		require.Equal(t, "HUMAN_DISMISSED", view.Payload["result"])
		if card.ChannelID == custom.ID {
			require.Equal(t, "52", view.Payload["source_message_id"])
		}
	}
}
