package governance

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (m *Module) memoryRuleInbox(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
	if req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.TenantID != authn.TenantIDFromContext(ctx) {
		return nil, ErrForbidden
	}
	if req.Kind != "MEMORY_RULE_APPROVAL" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.pendingRules == nil {
		return nil, ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	out := []InboxItem{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := m.pendingRules(ctx, tx, req.TenantID, req.ProjectID)
		if errors.Is(err, knowledge.ErrUnavailable) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		for _, row := range rows {
			item := InboxItem{InboxKey: InboxKey{Priority: "NORMAL", CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, Title: row.Title, ProjectID: row.ProjectID, ObjectType: "memory", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			item.SourceScope, item.SourcePrincipalID = row.Scope, row.AgentPrincipalID
			if req.After == nil || CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b InboxItem) int { return CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
