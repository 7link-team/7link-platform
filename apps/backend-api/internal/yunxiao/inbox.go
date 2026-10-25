package yunxiao

import (
	"context"
	"errors"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) registerWorklogInbox(s *module.Services) error {
	sources, err := module.Resolve[governance.InboxSources](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return sources.Register(governance.InboxSource{Kind: "WORKLOG_APPROVAL", Read: m.worklogInbox})
}

func (m *Module) worklogInbox(ctx context.Context, req governance.InboxRequest) ([]governance.InboxItem, error) {
	out := []governance.InboxItem{}
	if req.Kind != "WORKLOG_APPROVAL" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, governance.ErrInvalidArgument
	}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, governance.ErrForbidden
	}
	db := m.database()
	if db == nil {
		return nil, governance.ErrUnavailable
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
			return ErrUnavailable
		}
		if err := m.principals.Lock(ctx, tx, req.PrincipalID); err != nil {
			return err
		}
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, req.PrincipalID)
		if err != nil {
			return err
		}
		if !active || human.ID != req.PrincipalID {
			return ErrForbidden
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, req.TenantID, req.PrincipalID)
		if err != nil {
			return err
		}
		if !member {
			return ErrForbidden
		}
		q := query.BindWorklogs(tx, m.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.PrincipalID.Neq(req.PrincipalID), q.State.Eq("SUBMITTED"), q.ActorKind.Eq("HUMAN"))
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		for offset := 0; ; offset += 100 {
			rows, err := stmt.Order(q.CreatedAt, q.ID).Limit(100).Offset(offset).Find()
			if err != nil {
				return err
			}
			for _, r := range rows {
				for _, permission := range []string{"work_item_read", "worklog_approve"} {
					if err = m.projectAuthorityOn(ctx, tx, req.TenantID, r.ProjectID, permission); err != nil {
						break
					}
				}
				if errors.Is(err, ErrForbidden) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrInvalidMember) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, delivery.ErrWorkProjectReadOnly) {
					continue
				}
				if err != nil {
					return err
				}
				item := governance.InboxItem{InboxKey: governance.InboxKey{Priority: "NORMAL", CreatedAt: r.CreatedAt, Kind: req.Kind, ID: r.ID}, ProjectID: r.ProjectID, Title: r.WorkDate + " / " + r.Category, ObjectType: "worklog", ObjectID: r.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
				if req.After != nil && governance.CompareInboxKeys(item.InboxKey, *req.After) <= 0 {
					continue
				}
				out = append(out, item)
				if len(out) == req.Limit {
					return nil
				}
			}
			if len(rows) < 100 {
				return nil
			}
		}
	})
	if errors.Is(err, ErrUnavailable) {
		err = governance.ErrUnavailable
	}
	return out, err
}
