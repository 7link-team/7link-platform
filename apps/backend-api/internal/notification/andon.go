package notification

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) andonAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.decisionFacts.AndonOn == nil || m.decisionFacts.AndonEligibleOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["subject_id"])
	if err != nil {
		return false, ErrInvalid
	}
	f, found, err := m.decisionFacts.AndonOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || f.ProjectID != row.ProjectID || f.OccurrenceID.String() != params["pull_id"] {
		return false, err
	}
	yes, err := m.decisionFacts.AndonEligibleOn(ctx, tx, row.TenantID, id, actor)
	if err != nil || !yes {
		return false, err
	}
	if f.AccountableID == actor {
		return true, nil
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
}

// The existing durable work-state event is a wakeup. Current named-hold and
// immutable transition facts, never its audience or actor, authorize projection.
func (m *Module) projectAndonOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "work_item" {
		return ErrInvalid
	}
	if m.decisionFacts.AndonOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	f, found, err := m.decisionFacts.AndonOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found || f.ProjectID != e.ProjectID {
		return err
	}
	params := map[string]string{"subject_id": e.SubjectID.String(), "pull_id": f.OccurrenceID.String()}
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
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: "andon.pulled", Permission: "work_item_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: "andon.pulled", Severity: Urgent, Permission: "work_item_read", TemplateKey: "notification.andon_pulled", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/work-items/" + e.SubjectID.String(), DedupeKey: "andon:" + f.OccurrenceID.String()}, f.OccurrenceID, f.OccurrenceID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
