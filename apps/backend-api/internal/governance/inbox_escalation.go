package governance

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// escalationInbox reads unacknowledged real incidents. Acknowledgement closes
// the inbox alert; it does not resolve the condition or release source holds.
func (m *Module) escalationInbox(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, ErrForbidden
	}
	if req.Kind != "ESCALATION_ALERT" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	out := []InboxItem{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.authorizeHumanOn(ctx, tx, req.TenantID, "policy_read"); errors.Is(err, ErrForbidden) {
			return nil
		} else if err != nil {
			return err
		}
		q := query.BindEscalationIncidents(tx, m.seed.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.Eq("OPEN"))
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		rows, err := stmt.Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			allowed, err := m.canReadIncidentProject(ctx, tx, req.TenantID, row.ProjectID)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			allowed, err = m.escalationInboxRecipientOn(ctx, tx, req.TenantID, row.ProjectID, req.PrincipalID)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			item := InboxItem{InboxKey: InboxKey{Priority: "HIGH", CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, ProjectID: row.ProjectID, Title: row.Kind, Reason: row.Reason, ObjectType: "escalation_incident", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
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

func (m *Module) escalationInboxRecipientOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	owner, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: "project_takeover"})
	if err != nil || owner.Allowed {
		return owner.Allowed, err
	}
	if project <= 0 {
		return false, nil
	}
	role, err := m.roleIDOn(ctx, tx, tenant, "delivery_lead")
	if errors.Is(err, ErrRoleNotSeeded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	read, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: "project_read"})
	if err != nil || !read.Allowed {
		return false, err
	}
	for _, match := range read.Matches {
		if match.Effect == "allow" && match.RoleID == role.String() {
			return true, nil
		}
	}
	return false, nil
}
