package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EventSourceDocument contains source-owned canonical bytes, never caller text.
// OriginID identifies an immutable decision or the stable requirement aggregate.
type EventSourceDocument struct {
	Kind, Origin, Title, Content string
	OriginID                     guid.ID
	Version                      int
	WorkItemID                   *guid.ID
}
type EventSourceReader struct {
	ReadOn func(context.Context, *gorm.DB, events.Envelope) ([]EventSourceDocument, error)
}
type EventSourceReaderConnector struct{ Bind func(EventSourceReader) error }
type eventSourceBinding struct {
	sync.RWMutex
	readers  []EventSourceReader
	accepted ClientAcceptanceReader
}

type ClientAcceptanceReader struct {
	ReadOn func(context.Context, *gorm.DB, events.Envelope) ([]guid.ID, error)
}
type ClientAcceptanceReaderConnector struct {
	Bind func(ClientAcceptanceReader) error
}

func (m *Module) provideEventIngestion(s *module.Services) error {
	if err := module.Provide[ClientAcceptanceReaderConnector](s, ClientAcceptanceReaderConnector{Bind: func(reader ClientAcceptanceReader) error {
		if reader.ReadOn == nil {
			return ErrUnavailable
		}
		m.ingestion.Lock()
		defer m.ingestion.Unlock()
		if m.ingestion.accepted.ReadOn != nil {
			return ErrConflict
		}
		m.ingestion.accepted = reader
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[EventSourceReaderConnector](s, EventSourceReaderConnector{Bind: func(reader EventSourceReader) error {
		if reader.ReadOn == nil {
			return ErrUnavailable
		}
		m.ingestion.Lock()
		defer m.ingestion.Unlock()
		m.ingestion.readers = append(m.ingestion.readers, reader)
		return nil
	}})
}
func (m *Module) registerEventIngestion(s *module.Services) error {
	connector, err := module.Resolve[fleet.AgentRetirementObserverConnector](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	if err == nil {
		if err = connector.Bind(func(ctx context.Context, tx *gorm.DB, tenant, principal, eventID guid.ID) error {
			data, e := json.Marshal(map[string]guid.ID{"principal_id": principal})
			if e != nil {
				return e
			}
			return m.invalidateRetiredAgentSourcesOn(ctx, tx, events.Envelope{TenantID: tenant, EventID: eventID, EventType: "agent.retired", Subject: events.Subject{Kind: "agent", ID: principal}, Data: data})
		}); err != nil {
			return err
		}
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	// Transactional mode owns event receipts, rollback, aggregate parking and
	// replay. No second receipt or retry implementation belongs in this module.
	return catalog.AddSubscribers(Name, events.Subscriber{Name: "knowledge.source_ingest", Mode: events.Transactional, StartFrom: events.Earliest,
		EventTypes: []string{"requirement.versioned", "requirement.scope_changed", "work_item.state_changed", "project.stage_changed", "result.feedback_added", "agent.retired", "clientwork.package_decided"}, Handle: m.ingestEventOn})
}
func structuredSource(kind string) bool {
	return kind == "REQUIREMENT" || kind == "FEEDBACK" || kind == "DECISION" || kind == "DELIVERY"
}
func (m *Module) ingestEventOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if tx == nil || tx.Statement == nil || event.TenantID <= 0 || event.EventID <= 0 {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if event.EventType == "agent.retired" {
		return m.invalidateRetiredAgentSourcesOn(ctx, tx, event)
	}
	if event.ProjectID > 0 {
		archived, err := m.archivedProjectOn(ctx, tx, event.TenantID, event.ProjectID)
		if err != nil {
			return err
		}
		if archived {
			return nil
		}
	}
	if event.EventType == "clientwork.package_decided" {
		return m.acceptClientDeliverySourcesOn(ctx, tx, event)
	}
	m.ingestion.RLock()
	readers := append([]EventSourceReader{}, m.ingestion.readers...)
	m.ingestion.RUnlock()
	if len(readers) == 0 {
		return ErrUnavailable
	}
	for _, reader := range readers {
		documents, err := reader.ReadOn(ctx, tx, event)
		if err != nil {
			return err
		}
		for _, document := range documents {
			if err = m.upsertEventSourceOn(ctx, tx, event, document); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Module) acceptClientDeliverySourcesOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	m.ingestion.RLock()
	read := m.ingestion.accepted.ReadOn
	m.ingestion.RUnlock()
	if read == nil {
		return ErrUnavailable
	}
	ids, err := read(ctx, tx, event)
	if err != nil {
		return err
	}
	for _, work := range ids {
		q := query.BindSources(tx, m.names)
		var source model.Source
		if err = q.WithContext(ctx).UnderlyingDB().Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND project_id = ? AND source_kind = ? AND work_item_id = ?", event.TenantID, event.ProjectID, "DELIVERY", work).First(&source).Error; err != nil {
			return err
		}
		if source.ClientAccepted {
			continue
		}
		result := q.WithContext(ctx).UnderlyingDB().Where("tenant_id = ? AND id = ? AND version = ?", event.TenantID, source.ID, source.Version).Updates(map[string]any{"client_accepted": true, "version": source.Version + 1, "updated_at": m.now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		if err = m.recordIngestionOn(ctx, tx, event, source.ID, source.Version+1, "knowledge.source_synced", map[string]any{"client_accepted": true, "source_event_id": event.EventID}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) upsertEventSourceOn(ctx context.Context, tx *gorm.DB, event events.Envelope, doc EventSourceDocument) error {
	if !structuredSource(doc.Kind) || doc.OriginID <= 0 || doc.Version < 1 || event.ProjectID <= 0 || strings.TrimSpace(doc.Title) == "" || utf8.RuneCountInString(doc.Title) > 200 || doc.Content == "" || len(doc.Content) > 2000000 {
		return ErrInvalid
	}
	if (doc.Kind == "REQUIREMENT" && doc.Origin != "requirement") || ((doc.Kind == "FEEDBACK" || doc.Kind == "DECISION") && doc.Origin != "decision") || (doc.Kind == "DELIVERY" && doc.Origin != "work_item_delivery") || (doc.WorkItemID != nil && *doc.WorkItemID <= 0) {
		return ErrInvalid
	}
	q := query.BindSources(tx, m.names)
	// The typed source query supplies the resolved table; new origin columns do
	// not require exposing table names or importing another owner's storage.
	db := q.WithContext(ctx).UnderlyingDB()
	var row model.Source
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND origin_kind = ? AND origin_id = ? AND source_kind = ?", event.TenantID, doc.Origin, doc.OriginID, doc.Kind).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil && (row.ProjectID == nil || *row.ProjectID != event.ProjectID) {
		return ErrForbidden
	}
	if err == nil && row.OriginVersion >= doc.Version {
		return nil
	}
	now := m.now()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		id, e := m.nextSourceID()
		if e != nil {
			return e
		}
		row = model.Source{ID: id, TenantID: event.TenantID, Scope: "PROJECT", ProjectID: &event.ProjectID, SourceKind: doc.Kind,
			OriginKind: &doc.Origin, OriginID: &doc.OriginID, OriginVersion: doc.Version, WorkItemID: doc.WorkItemID,
			Title: doc.Title, Locator: fmt.Sprintf("%s:%s", doc.Origin, doc.OriginID), Format: "MARKDOWN", TagsJSON: "[]", State: "PENDING", Version: 1,
			CreatedBy: event.Actor.ID, UpdatedBy: event.Actor.ID, CreatedAt: now, UpdatedAt: now}
		if e = db.Create(&row).Error; e != nil {
			return e
		}
	}
	row.Title, row.Content, row.OriginVersion = doc.Title, doc.Content, doc.Version
	fragments, reason := chunkSource(normalizeSource(doc.Content, "MARKDOWN"), "MARKDOWN")
	if reason != "" {
		return ErrInvalid
	}
	if err = m.replaceSourceChunksOn(ctx, tx, event.TenantID, &row, fragments); err != nil {
		return err
	}
	row.State, row.StateReason, row.ContentHash = "INDEXED", "", sourceHash(normalizeSource(doc.Content, "MARKDOWN"))
	row.ChunkerVersion, row.ChunkCount, row.TextChars = sourceChunkerVersion, len(fragments), utf8.RuneCountInString(doc.Content)
	row.IndexedAt, row.UpdatedAt, row.UpdatedBy = &now, now, event.Actor.ID
	row.Version++
	if err = db.Where("tenant_id = ? AND id = ?", event.TenantID, row.ID).Save(&row).Error; err != nil {
		return err
	}
	return m.recordIngestionOn(ctx, tx, event, row.ID, row.Version, "knowledge.source_synced", map[string]any{"origin_kind": doc.Origin, "origin_id": doc.OriginID, "origin_version": doc.Version, "source_event_id": event.EventID})
}

// Canonical projection writes use source-event authority, so replay does not
// depend on whether the original actor still has interactive write permission.
func (m *Module) writeCanonicalChunkOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, source *model.Source, fragment sourceFragment) (EntryView, error) {
	if !structuredSource(source.SourceKind) {
		return m.writeOn(ctx, tx, tenant, EntryInput{Scope: sourceScope(source), Kind: "DOCUMENT", Title: source.Title, Body: fragment.Content, Provenance: source.Locator})
	}
	id, err := m.nextSourceID()
	if err != nil {
		return EntryView{}, err
	}
	now := m.now()
	live := 1
	row := model.Entry{ID: id, TenantID: tenant, Scope: source.Scope, ProjectID: source.ProjectID, Kind: "DOCUMENT", State: "ACTIVE", Title: source.Title, Body: fragment.Content,
		Provenance: source.Locator, Origin: "EVENT", ContentHash: sourceHash(source.Title + "\n" + fragment.Content), Version: 1, LiveSlot: &live, RiskFlags: "[]", CreatedBy: source.CreatedBy, CreatedAt: now, UpdatedAt: now}
	if err = query.BindEntries(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return EntryView{}, err
	}
	return entryView(&row), nil
}

func (m *Module) refreshCanonicalChunkTitleOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, source *model.Source, chunk *model.Chunk) error {
	if !structuredSource(source.SourceKind) {
		return nil
	}
	q := query.BindEntries(tx, m.names)
	entry, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(chunk.EntryID)).First()
	if err != nil {
		return err
	}
	if entry.Title == source.Title {
		return nil
	}
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(entry.ID), q.Version.Eq(entry.Version)).Updates(map[string]any{"title": source.Title, "content_hash": sourceHash(source.Title + "\n" + entry.Body), "version": entry.Version + 1, "updated_at": m.now()})
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func (m *Module) recordIngestionOn(ctx context.Context, tx *gorm.DB, source events.Envelope, id guid.ID, version int, typ string, detail map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, source.TenantID, typ, "knowledge_source", id)
	for key, value := range detail {
		e.Detail[key] = value
	}
	e.Detail["authority_source"], e.Detail["actor_kind"] = "SYSTEM:knowledge.source_ingest", "SYSTEM"
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: source.TenantID, ProjectID: source.ProjectID, Type: typ, Subject: events.Subject{Kind: "knowledge_source", ID: id}, SubjectVersion: int64(version), Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:knowledge.source_ingest"}, CausationID: source.EventID, Data: detail})
	return err
}
func (m *Module) invalidateRetiredAgentSourcesOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	var payload struct {
		PrincipalID guid.ID `json:"principal_id"`
	}
	if json.Unmarshal(event.Data, &payload) != nil || payload.PrincipalID <= 0 || payload.PrincipalID != event.Subject.ID {
		return ErrInvalid
	}
	q := query.BindSources(tx, m.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(event.TenantID), q.Scope.Eq("AGENT"), q.AgentPrincipalID.Eq(payload.PrincipalID), q.State.Neq("INVALIDATED")).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = m.invalidateSourceChunksOn(ctx, tx, event.TenantID, row, nil); err != nil {
			return err
		}
		version := row.Version
		row.Version++
		row.State, row.StateReason, row.ChunkCount = "INVALIDATED", "AGENT_RETIRED", 0
		row.UpdatedAt = m.now()
		if err = m.updateSourceOn(ctx, tx, row, version); err != nil {
			return err
		}
		if err = m.recordIngestionOn(ctx, tx, event, row.ID, row.Version, "knowledge.source_invalidated", map[string]any{"reason": "AGENT_RETIRED", "source_event_id": event.EventID}); err != nil {
			return err
		}
	}
	return nil
}
