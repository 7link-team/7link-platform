package governance

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindIntakeInbox(s *module.Services) error {
	source, err := module.Resolve[intake.PendingTriage](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.inbox.Register(InboxSource{Kind: "INTAKE_TRIAGE", Read: func(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
		if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID <= 0 || req.PrincipalID != authn.PrincipalIDFromContext(ctx) {
			return nil, ErrForbidden
		}
		if req.Kind != "INTAKE_TRIAGE" || req.ProjectID < 0 || req.Limit < 1 || req.Limit > 101 {
			return nil, ErrInvalidArgument
		}
		db, err := m.writer()
		if err != nil {
			return nil, err
		}
		out := []InboxItem{}
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			rows, err := source(ctx, tx, req.TenantID, req.ProjectID)
			if err != nil {
				return err
			}
			for _, row := range rows {
				item := InboxItem{InboxKey: InboxKey{Kind: req.Kind, ID: row.ID, Priority: "HIGH", CreatedAt: row.CreatedAt}, ProjectID: row.ProjectID, Title: row.Title, ObjectType: "intake", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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
	}})
}
