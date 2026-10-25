package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"gorm.io/gorm"
)

func (m *Module) notifyDispatchOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, recipient guid.ID) error {
	// Keep principal locking in the same order as Notify. The shared transaction
	// guard serializes competing projectors before checking permanent receipts.
	if m.guard == nil {
		return ErrUnavailable
	}
	if err := m.principal.LockActive(ctx, tx, recipient); err != nil {
		return err
	}
	if err := m.guard(ctx, tx, "notification:dispatch:"+e.TenantID.String()+":"+recipient.String()); err != nil {
		return err
	}
	q := query.BindDispatchReceipts(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ProposalID.Eq(e.SubjectID), q.RecipientPrincipalID.Eq(recipient)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if old != nil {
		return nil
	}
	ids, err := m.Notify(ctx, tx, Notification{
		TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{recipient},
		Type: "dispatch.confirmation_required", Severity: Action, TemplateKey: "notification.dispatch_confirmation_required",
		Permission: "work_item_assign", Params: map[string]string{"subject_id": e.SubjectID.String()},
		LinkPath:  "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/dispatch",
		DedupeKey: "dispatch:project:" + e.ProjectID.String(),
	})
	if err != nil || len(ids) == 0 {
		return err
	}
	return q.WithContext(ctx).Create(&model.DispatchReceipt{TenantID: e.TenantID, ProjectID: e.ProjectID, ProposalID: e.SubjectID, RecipientPrincipalID: recipient, NotificationID: ids[0], CreatedAt: m.now()})
}

// dispatchGroupAudienceOn recomputes the actionable aggregate from current
// source facts. A resolved first proposal cannot hide its pending siblings.
func (m *Module) dispatchGroupAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.collaborationFacts == nil {
		return false, ErrUnavailable
	}
	q := query.BindDispatchReceipts(tx, m.names)
	receipts, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.NotificationID.Eq(row.ID), q.RecipientPrincipalID.Eq(principal)).Order(q.ProposalID).Find()
	if err != nil {
		return false, err
	}
	// Notifications created before migration 00006 retain their original exact
	// source checks; no receipt or additional audience is invented for them.
	if len(receipts) == 0 {
		legacy := *row
		legacy.ID = 0
		return m.collaborationAudienceOn(ctx, tx, &legacy, principal)
	}
	count := 0
	var subject guid.ID
	for _, receipt := range receipts {
		fact, found, err := m.collaborationFacts(ctx, tx, row.TenantID, receipt.ProposalID, "dispatch")
		if err != nil {
			return false, err
		}
		if found && fact.ProjectID == row.ProjectID && fact.State == "PROPOSED" && fact.RequiresHuman && fact.ExpiresAt.After(m.now()) {
			allowed, err := m.dispatchRecipientOn(ctx, tx, row.TenantID, row.ProjectID, receipt.ProposalID, principal)
			if err != nil {
				return false, err
			}
			if !allowed {
				continue
			}
			count++
			subject = receipt.ProposalID
		}
	}
	if count == 0 {
		return false, nil
	}
	params, err := json.Marshal(map[string]string{"subject_id": subject.String(), "pending_count": strconv.Itoa(count)})
	if err != nil {
		return false, err
	}
	row.Params, row.MergeCount = string(params), count
	return true, nil
}

// A channel requester may observe its pending request without acquiring the
// assignment permission. The actual confirmation command still requires IAM.
func (m *Module) dispatchRecipientOn(ctx context.Context, tx *gorm.DB, tenant, project, proposal, principal guid.ID) (bool, error) {
	allowed, err := m.projectPermissionOn(ctx, tx, tenant, project, principal, "work_item_assign")
	if err != nil || allowed {
		return allowed, err
	}
	if m.dispatchInitiators == nil {
		return false, nil
	}
	fact, found, err := m.dispatchInitiators(ctx, tx, tenant, proposal)
	if err != nil || !found || fact.ProjectID != project || fact.RequesterID != principal {
		return false, err
	}
	if m.projectAuthority == nil {
		return false, ErrUnavailable
	}
	return m.projectAuthority(ctx, tx, tenant, project, principal)
}
