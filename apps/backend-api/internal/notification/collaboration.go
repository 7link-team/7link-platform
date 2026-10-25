package notification

import (
	"context"
	"encoding/json"
	"strings"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (m *Module) collaborationAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if row.Type == "dispatch.confirmation_required" && row.ID > 0 {
		return m.dispatchGroupAudienceOn(ctx, tx, row, principal)
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	kind := "package"
	if row.Type == "dispatch.confirmation_required" || row.Type == "dispatch.applied" {
		kind = "dispatch"
	}
	if row.Type == "preview.share_requested" || row.Type == "preview.share_decided" {
		kind = "preview"
		ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: row.TenantID, EntityID: row.ProjectID, PrincipalID: principal, SubjectType: authn.SubjectTypePrincipal})
	} else if row.Type == "project.tolerance_at_risk" {
		kind = "tolerance"
	}
	if m.collaborationFacts == nil {
		return false, ErrUnavailable
	}
	fact, found, err := m.collaborationFacts(ctx, tx, row.TenantID, id, kind)
	if err != nil || !found || fact.ProjectID != row.ProjectID {
		return false, err
	}
	if kind == "preview" {
		if row.Type == "preview.share_requested" {
			return fact.State == "PENDING_APPROVAL" && fact.ApprovalEligible && fact.RequesterID != principal, nil
		}
		return fact.RequesterID == principal && (fact.Decision == "ACTIVE" || fact.Decision == "REJECTED"), nil
	}
	if kind == "tolerance" {
		if fact.State != "AT_RISK" {
			return false, nil
		}
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	}
	if kind == "dispatch" {
		if row.Type == "dispatch.applied" {
			if fact.State != "APPLIED" || !fact.Automatic || fact.WorkItemID <= 0 || fact.AssignmentID <= 0 || fact.AppliedID <= 0 {
				return false, nil
			}
			return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
		}
		if fact.State != "PROPOSED" || !fact.RequiresHuman || !fact.ExpiresAt.After(m.now()) {
			return false, nil
		}
		return m.dispatchRecipientOn(ctx, tx, row.TenantID, row.ProjectID, id, principal)
	}
	if !packageReceiptMatches(fact, row.Type) {
		return false, nil
	}
	if m.subjectHumanActor == nil {
		return false, ErrUnavailable
	}
	publisher, found, err := m.subjectHumanActor(ctx, tx, row.TenantID, row.ProjectID, "clientwork", id, "clientwork.package_published")
	if err != nil {
		return false, err
	}
	if found && publisher == principal {
		return true, nil
	}
	lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	if err != nil || lead {
		return lead, err
	}
	if row.Type != "delivery.client_rejected" {
		return false, nil
	}
	if m.workFacts.ProjectModeOn == nil {
		return false, ErrUnavailable
	}
	mode, err := m.workFacts.ProjectModeOn(ctx, tx, row.TenantID, row.ProjectID)
	if err != nil || mode != "AUTONOMOUS" {
		return false, err
	}
	return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
}

func packageReceiptMatches(fact clientwork.CollaborationNotificationFact, typ string) bool {
	if typ == "delivery.receipt_received" {
		return fact.State == "ACCEPTED" && fact.Decision == "ACCEPT"
	}
	return typ == "delivery.client_rejected" && ((fact.State == "REJECTED" && fact.Decision == "REJECT") || (fact.State == "PARTIALLY_ACCEPTED" && fact.Decision == "PARTIALLY_ACCEPTED"))
}

func (m *Module) projectCollaborationEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "clientwork" {
		return ErrInvalid
	}
	if m.collaborationFacts == nil || m.candidates == nil {
		return ErrUnavailable
	}
	kind, typ, permission, page, severity := "package", "delivery.receipt_received", "delivery_read", "deliveries", Info
	if e.Type == "clientwork.dispatch_proposed" {
		kind, typ, permission, page, severity = "dispatch", "dispatch.confirmation_required", "work_item_assign", "dispatch", Action
	}
	switch e.Type {
	case "clientwork.dispatch_decided":
		kind, typ, permission, page, severity = "dispatch", "dispatch.applied", "work_item_read", "dispatch", Info
	case "clientwork.preview_share_requested":
		kind, typ, permission, page, severity = "preview", "preview.share_requested", "preview_share_approve", "previews", Action
	case "clientwork.preview_share_decided":
		kind, typ, permission, page, severity = "preview", "preview.share_decided", "project_read", "previews", Info
	case "clientwork.tolerance_forecasted", "clientwork.tolerance_changed":
		kind, typ, permission, page, severity = "tolerance", "project.tolerance_at_risk", "project_read", "exceptions", Action
	}
	fact, found, err := m.collaborationFacts(ctx, tx, e.TenantID, e.SubjectID, kind)
	if err != nil || !found || fact.ProjectID != e.ProjectID {
		return err
	}
	if kind == "package" {
		if fact.Decision != "ACCEPT" {
			typ, severity = "delivery.client_rejected", Action
		}
		if !packageReceiptMatches(fact, typ) {
			return nil
		}
	} else if kind == "dispatch" && typ == "dispatch.confirmation_required" && (fact.State != "PROPOSED" || !fact.RequiresHuman || !fact.ExpiresAt.After(m.now())) {
		return nil
	}
	dedupe := typ + ":" + e.SubjectID.String()
	if kind == "tolerance" {
		dedupe += ":" + m.now().UTC().Format("2006-01-02")
	}
	linkPath := "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/" + page + "?id=" + e.SubjectID.String()
	if kind == "preview" {
		linkPath = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/previews?collection=shares&id=" + e.SubjectID.String()
	}
	params := map[string]string{"subject_id": e.SubjectID.String()}
	raw, _ := json.Marshal(params)
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Type: typ, Permission: permission, Params: string(raw)}
			allowed, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			if typ == "dispatch.confirmation_required" {
				if err = m.notifyDispatchOn(ctx, tx, e, id); err != nil {
					return err
				}
				continue
			}
			if typ == "dispatch.applied" {
				err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: severity, TemplateKey: "notification.dispatch_applied", Permission: permission, Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/dispatch", DedupeKey: "dispatch-applied:" + e.ProjectID.String()}, e.SubjectID, e.SubjectID)
				if err != nil {
					return err
				}
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: severity, TemplateKey: "notification." + strings.ReplaceAll(typ, ".", "_"), Permission: permission, Params: params, LinkPath: linkPath, DedupeKey: dedupe})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
