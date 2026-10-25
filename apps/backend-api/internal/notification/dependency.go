package notification

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

const dependencyCanceledType = "work_item.dependency_cancelled"             //nolint:misspell // Canonical notification code.
const dependencyCanceledTemplate = "notification.work_dependency_cancelled" //nolint:misspell // Canonical translation key.

func (m *Module) dependencyAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.decisionFacts.CancelledDependentsOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	pred, err := guid.RequireID(params["predecessor_id"])
	if err != nil {
		return false, ErrInvalid
	}
	facts, err := m.decisionFacts.CancelledDependentsOn(ctx, tx, row.TenantID, pred)
	if err != nil {
		return false, err
	}
	for _, fact := range facts {
		if fact.ProjectID != row.ProjectID || fact.WorkItemID.String() != params["subject_id"] {
			continue
		}
		if fact.ExecutorID == actor {
			return true, nil
		}
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	}
	return false, nil
}

func (m *Module) projectCancelledDependentsOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if m.decisionFacts.CancelledDependentsOn == nil {
		return ErrUnavailable
	}
	facts, err := m.decisionFacts.CancelledDependentsOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || len(facts) == 0 {
		return err
	}
	if m.candidates == nil {
		return ErrUnavailable
	}
	for _, fact := range facts {
		if fact.ProjectID != e.ProjectID || fact.WorkItemID <= 0 {
			continue
		}
		params := map[string]string{"subject_id": fact.WorkItemID.String(), "predecessor_id": e.SubjectID.String()}
		raw, _ := json.Marshal(params)
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
				row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: dependencyCanceledType, Permission: "work_item_read", Params: string(raw)}
				allowed, err := m.visibleOn(ctx, tx, row, actor)
				if err != nil {
					return err
				}
				if !allowed {
					continue
				}
				_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: row.Type, Severity: Info, TemplateKey: dependencyCanceledTemplate, Permission: row.Permission, Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/work-items?id=" + fact.WorkItemID.String(), DedupeKey: "depc:" + fact.WorkItemID.String() + ":" + e.SubjectID.String()})
				if err != nil {
					return err
				}
			}
			if len(ids) < 100 {
				break
			}
		}
	}
	return nil
}
