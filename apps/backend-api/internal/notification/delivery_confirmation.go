package notification

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) deliveryConfirmationAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.deliveryConfirmations.ReadOn == nil || m.deliveryConfirmations.EligibleOn == nil {
		return false, ErrUnavailable
	}
	var p map[string]string
	if json.Unmarshal([]byte(row.Params), &p) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(p["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	fact, found, err := m.deliveryConfirmations.ReadOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || fact.ProjectID != row.ProjectID || fact.PackageID.String() != p["package_id"] {
		return false, err
	}
	return m.deliveryConfirmations.EligibleOn(ctx, tx, row.TenantID, id, actor)
}

func (m *Module) projectDeliveryConfirmationOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if !validTransaction(tx) || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "delivery_confirmation" {
		return ErrInvalid
	}
	if e.Type == "delivery.withdrawn" {
		return nil
	}
	if m.deliveryConfirmations.ReadOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	fact, found, err := m.deliveryConfirmations.ReadOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || fact.ProjectID != e.ProjectID {
		return err
	}
	params := map[string]string{"subject_id": fact.ID.String(), "package_id": fact.PackageID.String()}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, actor := range ids {
			if actor <= after {
				return ErrInvalid
			}
			after = actor
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: "delivery.confirmation_requested", Permission: "delivery_send", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: row.Type, Severity: Action, Permission: row.Permission, TemplateKey: "notification.delivery_confirmation", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/deliveries", DedupeKey: "delivery_confirmation:" + fact.ID.String()}, fact.ID, fact.ID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
