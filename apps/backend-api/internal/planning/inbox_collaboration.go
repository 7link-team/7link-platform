package planning

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/governance"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) registerCollaborationInbox(s *module.Services, sources governance.InboxSources) error {
	var err error
	m.collaborationInbox, err = module.Resolve[clientwork.CollaborationInbox](s)
	if err != nil {
		return err
	}
	for _, kind := range []string{"DISPATCH_PROPOSAL", "EXCEPTION_DECISION", "PREVIEW_SHARE_APPROVAL", "CLARIFICATION_REPLY"} {
		if err := sources.Register(governance.InboxSource{Kind: kind, Read: m.readCollaborationInbox}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) readCollaborationInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, ErrForbidden
	}
	if !slices.Contains([]string{"DISPATCH_PROPOSAL", "EXCEPTION_DECISION", "PREVIEW_SHARE_APPROVAL", "CLARIFICATION_REPLY"}, req.Kind) || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalid
	}
	if m.collaborationInbox == nil {
		return nil, governance.ErrUnavailable
	}
	db, err := m.database()
	if err != nil {
		return nil, governance.ErrUnavailable
	}
	out := []governance.InboxItem{}
	read := func(tx *gorm.DB) error {
		rows, err := m.collaborationInbox(ctx, tx, req.TenantID, req.ProjectID, req.Kind)
		if errors.Is(err, clientwork.ErrUnavailable) || errors.Is(err, ErrUnavailable) {
			return governance.ErrUnavailable
		}
		if err != nil {
			return err
		}
		for _, row := range rows {
			objectType := map[string]string{"DISPATCH_PROPOSAL": "dispatch_proposal", "EXCEPTION_DECISION": "tolerance_exception", "PREVIEW_SHARE_APPROVAL": "preview_share", "CLARIFICATION_REPLY": "intake"}[req.Kind]
			priority := "NORMAL"
			if req.Kind == "EXCEPTION_DECISION" || req.Kind == "CLARIFICATION_REPLY" {
				priority = "HIGH"
			}
			item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: priority, DueAt: row.DueAt, CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, ProjectID: row.ProjectID, Title: req.Kind + " 路 " + row.ID.String(), ObjectType: objectType, ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
			if req.Kind == "CLARIFICATION_REPLY" {
				item.ObjectID = row.IntakeID
				item.SourceRevision = row.Revision
			}
			if req.After == nil || governance.CompareInboxKeys(item.InboxKey, *req.After) > 0 {
				out = append(out, item)
			}
		}
		return nil
	}
	if req.Transaction != nil {
		err = read(req.Transaction)
	} else {
		err = db.WithContext(ctx).Transaction(read)
	}
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b governance.InboxItem) int { return governance.CompareInboxKeys(a.InboxKey, b.InboxKey) })
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}
