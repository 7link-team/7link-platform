package collab

import (
	activityi18n "7link.com/platform/internal/collab/i18n"
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
)

// ChannelPoster is a trusted module capability, never an HTTP operation.
// Its caller owns the authorization and source object transaction.
type ChannelPoster struct {
	PostSystemOn func(context.Context, *gorm.DB, SystemCardInput) (MessageView, error)
}
type SystemCardInput struct {
	TenantID, ProjectID, ChannelID, SourceID guid.ID
	CausationID                              guid.ID
	Kind, Body, FoldKey                      string
	Attention                                bool
	Payload                                  map[string]any
}

func (m *Module) provideSystemCards(s *module.Services) error {
	return module.Provide[ChannelPoster](s, ChannelPoster{PostSystemOn: m.postSystemCardOn})
}
func (m *Module) postSystemCardOn(ctx context.Context, tx *gorm.DB, in SystemCardInput) (MessageView, error) {
	var empty MessageView
	if e := checkTransaction(tx, in.TenantID, in.ProjectID); e != nil {
		return empty, e
	}
	if in.SourceID <= 0 || len(in.Body) > 8000 || strings.TrimSpace(in.Body) == "" || len(in.FoldKey) > 200 || (in.Kind != "EVENT" && in.Kind != "DECISION_CARD" && in.Kind != "DISPATCH_CARD" && in.Kind != "SEARCH_CARD") {
		return empty, ErrInvalidArgument
	}
	payload, e := json.Marshal(in.Payload)
	if e != nil || len(payload) > 32000 {
		return empty, ErrInvalidArgument
	}
	q := query.BindChannels(tx, m.names)
	request := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.ArchivedAt.IsNull())
	if in.ChannelID > 0 {
		request = request.Where(q.ID.Eq(in.ChannelID))
	} else {
		request = request.Where(q.Kind.Eq("PROJECT_DEFAULT"))
	}
	ch, e := request.First()
	if e != nil {
		return empty, e
	}
	var existing model.SystemCard
	e = query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: in.TenantID, ChannelID: ch.ID, SourceID: in.SourceID}).First(&existing).Error
	if e == nil && in.Kind == "DISPATCH_CARD" {
		var merged map[string]any
		if e = json.Unmarshal([]byte(existing.Payload), &merged); e != nil {
			return empty, e
		}
		for key, value := range in.Payload {
			merged[key] = value
		}
		encoded, e := json.Marshal(merged)
		if e != nil {
			return empty, e
		}
		if string(encoded) != existing.Payload || existing.Attention != in.Attention {
			if e = query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: in.TenantID, MessageID: existing.MessageID}).Updates(map[string]any{"payload": string(encoded), "attention": in.Attention}).Error; e != nil {
				return empty, e
			}
			existing.Payload = string(encoded)
			existing.Attention = in.Attention
			if e = m.appendAudit(ctx, tx, in.TenantID, in.ProjectID, ch.ID, "collab.system_updated", 0, ProvisioningAudit{AuthoritySource: "SYSTEM:event_projector"}); e != nil {
				return empty, e
			}
			if _, e = m.publisher.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "collab.system_updated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:event_projector"}, Subject: events.Subject{Kind: "message", ID: existing.MessageID}, SubjectVersion: 1, CausationID: in.CausationID, ExtraTopics: conversationTopics(in.TenantID, ch.ID, nil), Data: map[string]any{"channel_id": ch.ID.String()}}); e != nil {
				return empty, e
			}
		}
	}
	if e == nil {
		mq := query.BindMessages(tx, m.names)
		row, e := mq.WithContext(ctx).Where(mq.ID.Eq(existing.MessageID), mq.TenantID.Eq(in.TenantID)).First()
		if e != nil {
			return empty, e
		}
		view := messageView(row)
		applySystemCard(&view, existing)
		return view, nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return empty, e
	}
	now := m.now()
	// System facts default to INTERNAL even in a client-shared channel. Public
	// projection needs its own explicit source policy; it is never inferred here.
	row := model.Message{ID: guid.ID(id), TenantID: in.TenantID, ChannelID: ch.ID, Seq: ch.LastSeq + 1, ContextSeq: ch.LastSeq + 1, Body: in.Body, Visibility: "INTERNAL", Version: 1, CreatedAt: now, UpdatedAt: now}
	if e = query.BindMessages(tx, m.names).WithContext(ctx).Create(&row); e != nil {
		return empty, e
	}
	card := model.SystemCard{MessageID: row.ID, TenantID: in.TenantID, ChannelID: ch.ID, SourceID: in.SourceID, Kind: in.Kind, Payload: string(payload), FoldKey: in.FoldKey, Attention: in.Attention}
	if e = query.BindSystemCards(tx, m.names).Create(&card).Error; e != nil {
		return empty, e
	}
	changed, e := q.WithContext(ctx).Where(q.ID.Eq(ch.ID), q.TenantID.Eq(in.TenantID), q.LastSeq.Eq(ch.LastSeq)).Updates(map[string]any{"last_seq": row.Seq, "last_message_at": now, "updated_at": now, "version": ch.Version + 1})
	if e != nil {
		return empty, e
	}
	if changed.RowsAffected != 1 {
		return empty, ErrChannelVersion
	}
	if e = m.appendAudit(ctx, tx, in.TenantID, in.ProjectID, ch.ID, "collab.system_posted", 0, ProvisioningAudit{AuthoritySource: "SYSTEM:event_projector"}); e != nil {
		return empty, e
	}
	if _, e = m.publisher.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "collab.system_posted", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:event_projector"}, Subject: events.Subject{Kind: "message", ID: row.ID}, SubjectVersion: 1, CausationID: in.CausationID, ExtraTopics: conversationTopics(in.TenantID, ch.ID, nil), Data: map[string]any{"channel_id": ch.ID.String()}}); e != nil {
		return empty, e
	}
	view := messageView(&row)
	applySystemCard(&view, card)
	return view, nil
}
func applySystemCard(view *MessageView, card model.SystemCard) {
	view.Kind = card.Kind
	view.FoldKey = card.FoldKey
	view.Attention = card.Attention
	_ = json.Unmarshal([]byte(card.Payload), &view.Payload)
}
func (m *Module) hydrateSystemCardsOn(tx *gorm.DB, tenant guid.ID, items []MessageView) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]guid.ID, 0, len(items))
	positions := map[guid.ID]int{}
	for i, item := range items {
		ids = append(ids, item.ID)
		positions[item.ID] = i
	}
	var cards []model.SystemCard
	if e := query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: tenant}).Where("message_id IN ?", ids).Find(&cards).Error; e != nil {
		return e
	}
	for _, card := range cards {
		applySystemCard(&items[positions[card.MessageID]], card)
	}
	return nil
}

func (m *Module) injectSystemProjector(s *module.Services) error {
	catalog, e := module.Resolve[events.SubscriberCatalog](s)
	if e != nil {
		return e
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_collab_event_projector_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_collab_event_projector_description", Name: "collab.event_projector", EventTypes: []string{"execution.state_changed", "execution.verification_completed", "work_item.state_changed", "work_item.attention_started", "budget.threshold_reached", "clientwork.dispatch_proposed", "clientwork.dispatch_decided", "clientwork.dispatch_expired"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.projectSystemEventOn})
}
func (m *Module) projectSystemEventOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if event.ProjectID <= 0 || event.TenantID <= 0 {
		return nil
	}
	data := map[string]any{}
	if e := json.Unmarshal(event.Data, &data); e != nil {
		return e
	}
	status, _ := data["to"].(string)
	if status == "" {
		status, _ = data["state"].(string)
	}
	if status == "" {
		status, _ = data["verdict"].(string)
	}
	kind := "EVENT"
	attention := strings.Contains(status, "FAILED") || event.EventType == "work_item.attention_started" || event.EventType == "budget.threshold_reached"
	payload := map[string]any{"event_type": event.EventType, "ref_kind": event.Subject.Kind, "ref_id": event.Subject.ID.String(), "status": status, "project_id": event.ProjectID.String()}
	if event.EventType == "work_item.state_changed" && status == "NEEDS_HUMAN_REVIEW" {
		kind = "DECISION_CARD"
	}
	if strings.HasPrefix(event.EventType, "clientwork.dispatch_") {
		kind = "DISPATCH_CARD"
		if m.channelDispatchFacts == nil {
			return ErrUnavailable
		}
		facts, err := m.channelDispatchFacts(ctx, tx, event.TenantID, event.ProjectID, event.Subject.ID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for key, value := range facts {
			payload[key] = value
		}
		attention = payload["result"] == "NO_ELIGIBLE"
	}
	sourceID := event.EventID
	if kind == "DISPATCH_CARD" {
		sourceID = event.Subject.ID
	}
	if kind == "DISPATCH_CARD" {
		var cards []model.SystemCard
		if err := query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: event.TenantID, SourceID: sourceID, Kind: kind}).Find(&cards).Error; err != nil {
			return err
		}
		for _, card := range cards {
			if _, err := m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: event.TenantID, ProjectID: event.ProjectID, ChannelID: card.ChannelID, SourceID: sourceID, CausationID: event.EventID, Kind: kind, Body: event.EventType, FoldKey: card.FoldKey, Attention: attention, Payload: payload}); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
	}
	_, e := m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: event.TenantID, ProjectID: event.ProjectID, SourceID: sourceID, CausationID: event.EventID, Kind: kind, Body: event.EventType, FoldKey: event.Subject.Kind + ":" + event.Subject.ID.String(), Attention: attention, Payload: payload})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil
	}
	return e
}
