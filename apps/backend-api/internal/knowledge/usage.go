package knowledge

import (
	"context"
	"errors"
	"sync"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UsageFact references an actual persisted execution inclusion or output
// citation. The source owner must verify the exact entry/hash and current actor
// inside the caller's transaction; caller-provided IDs alone are insufficient.
type UsageFact struct {
	EntryID     guid.ID
	ExecutionID guid.ID
	EvidenceID  guid.ID
	Kind        string
	ContentHash string
}
type UsageEvidence struct {
	VerifyOn func(context.Context, *gorm.DB, guid.ID, UsageFact) error
}
type UsageEvidenceConnector struct{ Bind func(UsageEvidence) error }
type UsageRecorder struct {
	RecordOn func(context.Context, *gorm.DB, guid.ID, UsageFact) error
}
type usageEvidenceBinding struct {
	sync.RWMutex
	evidence UsageEvidence
}
type SourceUsage struct {
	Available  bool   `json:"available"`
	Inclusions *int64 `json:"inclusions,omitempty" doc:"Distinct persisted runtime inclusion receipts; absent means unavailable, not zero measured usage."`
	Citations  *int64 `json:"citations,omitempty" doc:"Distinct verified output citation receipts; diagnostics do not increment this counter."`
	Reason     string `json:"reason,omitempty"`
}

func (m *Module) provideUsage(s *module.Services) error {
	if err := module.Provide[UsageRecorder](s, UsageRecorder{RecordOn: m.recordUsageOn}); err != nil {
		return err
	}
	return module.Provide[UsageEvidenceConnector](s, UsageEvidenceConnector{Bind: func(e UsageEvidence) error {
		if e.VerifyOn == nil {
			return ErrUnavailable
		}
		m.usage.Lock()
		defer m.usage.Unlock()
		if m.usage.evidence.VerifyOn != nil {
			return ErrConflict
		}
		m.usage.evidence = e
		return nil
	}})
}
func (m *Module) recordUsageOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in UsageFact) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || in.EntryID <= 0 || in.ExecutionID <= 0 || in.EvidenceID <= 0 || len(in.ContentHash) != 64 || (in.Kind != "INCLUSION" && in.Kind != "CITATION") {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	m.usage.RLock()
	verify := m.usage.evidence.VerifyOn
	m.usage.RUnlock()
	if verify == nil {
		return ErrUnavailable
	}
	if err := verify(ctx, tx, tenant, in); err != nil {
		return err
	}
	if handled, err := m.recordInjectionFactOn(ctx, tx, tenant, in); err != nil || handled {
		return err
	}
	q := query.BindEntries(tx, m.names)
	entry, err := q.WithContext(ctx).Select(q.Scope, q.ProjectID, q.AgentPrincipalID).Where(q.TenantID.Eq(tenant), q.ID.Eq(in.EntryID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	kind, err := m.authorize(ctx, tx, tenant, Scope{Scope: entry.Scope, ProjectID: entry.ProjectID, AgentPrincipalID: entry.AgentPrincipalID}, "knowledge_read", false)
	if err != nil {
		return err
	}
	entry, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(in.EntryID)).First()
	if err != nil {
		return err
	}
	var sourceID guid.ID
	if in.Kind == "CITATION" {
		// A delivered snapshot stays historical fact after source replacement or
		// chunk retention. Current scope authorization above is still mandatory.
		var inclusion model.UsageReceipt
		err = query.BindUsageReceipts(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "entry_id": in.EntryID, "execution_id": in.ExecutionID, "kind": "INCLUSION", "content_hash": in.ContentHash}).First(&inclusion).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		sourceID = inclusion.SourceID
	} else {
		if entry.ContentHash != in.ContentHash || !m.memoryActive(entry) {
			return ErrConflict
		}
		chunks := query.BindChunks(tx, m.names)
		chunk, err := chunks.WithContext(ctx).Where(chunks.TenantID.Eq(tenant), chunks.EntryID.Eq(entry.ID), chunks.InvalidatedAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		sourceID = chunk.SourceID
	}

	id, err := m.nextSourceID()
	if err != nil {
		return err
	}
	row := model.UsageReceipt{ID: id, TenantID: tenant, SourceID: sourceID, EntryID: entry.ID, ExecutionID: in.ExecutionID, Kind: in.Kind, EvidenceID: in.EvidenceID, ContentHash: in.ContentHash, CreatedAt: m.now()}
	result := query.BindUsageReceipts(tx, m.names).WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if in.Kind == "INCLUSION" {
		if err = m.memoryUsageOn(ctx, tx, entry); err != nil {
			return err
		}
	}
	return m.recordLifecycle(ctx, tx, tenant, projectID(Scope{ProjectID: entry.ProjectID}), id, "knowledge_usage", 1, kind, "knowledge_read", "knowledge.usage_recorded", map[string]any{"source_id": sourceID, "entry_id": entry.ID, "execution_id": in.ExecutionID, "evidence_id": in.EvidenceID, "kind": in.Kind})
}
func (m *Module) sourceUsageOn(ctx context.Context, tx *gorm.DB, tenant, source guid.ID) (SourceUsage, error) {
	out := SourceUsage{Reason: "RUNTIME_USAGE_RECEIPTS_UNAVAILABLE"}
	if _, _, err := m.sourceOn(ctx, tx, tenant, source, "knowledge_read", false); err != nil {
		return out, err
	}
	var inclusions, citations int64
	base := func() *gorm.DB {
		return query.BindUsageReceipts(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "source_id": source})
	}
	if err := base().Where(map[string]any{"kind": "INCLUSION"}).Count(&inclusions).Error; err != nil {
		return out, err
	}
	if err := base().Where(map[string]any{"kind": "CITATION"}).Count(&citations).Error; err != nil {
		return out, err
	}
	if inclusions+citations > 0 {
		out.Available = true
		out.Inclusions = &inclusions
		out.Citations = &citations
		out.Reason = ""
	}
	return out, nil
}
