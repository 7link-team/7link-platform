package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ContextItem is a frozen reference document, not an instruction or evidence of
// citation. The execution owner persists these exact bytes before delivery.
type ContextItem struct {
	RetrievalLogID guid.ID `json:"retrieval_log_id,omitempty"`
	SnapshotHash   string  `json:"snapshot_hash,omitempty" doc:"SHA256 of title plus the budgeted body; content_hash identifies the canonical original."`
	EntryID        guid.ID `json:"entry_id"`
	Version        int     `json:"version"`
	Label          string  `json:"label"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	ContentHash    string  `json:"content_hash"`
}
type RuntimeContext struct {
	AuthorizeSnapshotOn func(context.Context, *gorm.DB, guid.ID, guid.ID, []ContextItem) error
	BuildInjectionOn    func(context.Context, *gorm.DB, InjectionRequest) (InjectionResult, error)
	ResolveOn           func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]ContextItem, error)
	ValidateOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, []ContextItem) error
}

func (m *Module) provideRuntimeContext(s *module.Services) error {
	return module.Provide[RuntimeContext](s, RuntimeContext{AuthorizeSnapshotOn: m.authorizeSnapshotOn, BuildInjectionOn: m.buildInjectionOn, ResolveOn: m.contextItemsOn, ValidateOn: m.validateContextItemsOn})
}

// authorizeSnapshotOn checks who may read retained bytes. Revocation, versions
// and chunk availability affect runtime validity, not historical read authority.
func (m *Module) authorizeSnapshotOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, items []ContextItem) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	seen := map[guid.ID]bool{}
	for _, item := range items {
		if item.EntryID <= 0 {
			return ErrInvalid
		}
		if seen[item.EntryID] {
			continue
		}
		seen[item.EntryID] = true
		q := query.BindEntries(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.EntryID)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) && item.RetrievalLogID > 0 {
			var log model.InjectionLog
			if err = query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": item.RetrievalLogID, "project_id": project}).First(&log).Error; err != nil {
				return err
			}
			var frozen InjectionResult
			if err = json.Unmarshal([]byte(log.ResultJSON), &frozen); err != nil {
				return err
			}
			matched := false
			for _, stored := range append(frozen.Rules, frozen.Items...) {
				if stored.ContextItem != item {
					continue
				}
				matched = true
				scope := Scope{Scope: stored.Scope}
				if stored.Scope == "PROJECT" || stored.Scope == "AGENT" {
					scope.ProjectID = &log.ProjectID
				}
				if stored.Scope == "AGENT" {
					scope.AgentPrincipalID = &log.AgentPrincipalID
					// Historical logs did not distinguish global from project Agent
					// scope. Require both policies rather than broadening either.
					global := scope
					global.ProjectID = nil
					if _, err = m.authorize(ctx, tx, tenant, global, "knowledge_read", false); err != nil {
						return err
					}
				}
				if _, err = m.authorize(ctx, tx, tenant, scope, "knowledge_read", false); err != nil {
					return err
				}
				break
			}
			if !matched {
				return ErrForbidden
			}
			continue
		}
		if err != nil {
			return err
		}
		if row.ProjectID != nil && *row.ProjectID != project {
			return ErrForbidden
		}
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, "knowledge_read", false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) contextItemsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]ContextItem, error) {
	out := []ContextItem{}
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || len(ids) > 50 {
		return nil, ErrInvalid
	}
	seen := map[guid.ID]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrInvalid
		}
		seen[id] = true
		q := query.BindEntries(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
		if err != nil {
			return nil, err
		}
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, "knowledge_read", false); err != nil {
			return nil, err
		}
		if row.ProjectID != nil && *row.ProjectID != project {
			return nil, ErrForbidden
		}
		if !m.memoryActive(row) {
			return nil, ErrConflict
		}
		// Only source-backed indexed entries can currently produce usage receipts.
		c := query.BindChunks(tx, m.names)
		if _, err = c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.EntryID.Eq(id), c.InvalidatedAt.IsNull()).First(); err != nil {
			return nil, err
		}
		out = append(out, ContextItem{EntryID: id, Version: row.Version, Label: fmt.Sprintf("[K%d]", len(out)+1), Title: row.Title, Body: row.Body, ContentHash: row.ContentHash})
	}
	return out, nil
}
func (m *Module) validateContextItemsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, items []ContextItem) error {
	if len(items) > 0 && items[0].RetrievalLogID > 0 {
		_, stored, err := m.injectionLogOn(ctx, tx, tenant, items[0].RetrievalLogID, false)
		if err != nil {
			return err
		}
		all := append(append([]InjectionItem{}, stored.Rules...), stored.Items...)
		if len(all) != len(items) {
			return ErrConflict
		}
		for i, item := range items {
			if item != all[i].ContextItem {
				return ErrConflict
			}
		}
		var log model.InjectionLog
		if err = query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": stored.RetrievalLogID}).First(&log).Error; err != nil {
			return err
		}
		return m.validateInjectionOn(ctx, tx, tenant, project, log.ExecutionID, stored)
	}
	ids := make([]guid.ID, len(items))
	for i, item := range items {
		ids[i] = item.EntryID
	}
	current, err := m.contextItemsOn(ctx, tx, tenant, project, ids)
	if err != nil {
		return err
	}
	for i, item := range items {
		if item != current[i] {
			return ErrConflict
		}
	}
	return nil
}
