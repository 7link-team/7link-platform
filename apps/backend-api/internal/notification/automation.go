package notification

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) automationAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if row.ID > 0 {
		return m.sourceGroupAudienceOn(ctx, tx, row, actor)
	}
	if m.automationFacts.RunOn == nil || m.automationFacts.EligibleOn == nil {
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
	fact, found, err := m.automationFacts.RunOn(ctx, tx, row.TenantID, id)
	if err != nil || !found || fact.ID != id || fact.ProjectID != row.ProjectID {
		return false, err
	}
	return m.automationFacts.EligibleOn(ctx, tx, row.TenantID, id, actor)
}

func (m *Module) projectAutomationFailureOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if !validTransaction(tx) || e.Type != "automation.run_failed" || e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "automation_run" {
		return ErrInvalid
	}
	if m.automationFacts.RunOn == nil || m.automationFacts.EligibleOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	fact, found, err := m.automationFacts.RunOn(ctx, tx, e.TenantID, e.SubjectID)
	if err != nil || !found {
		return err
	}
	if fact.ID != e.SubjectID || fact.ProjectID != e.ProjectID || fact.RuleID <= 0 || fact.CreatorID <= 0 || fact.WorkItemID <= 0 {
		return ErrInvalid
	}
	params := map[string]string{"subject_id": fact.ID.String(), "rule_id": fact.RuleID.String(), "work_item_id": fact.WorkItemID.String(), "rule_name": fact.RuleName, "error": fact.Error, "source_count": "1"}
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
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: actor, Type: e.Type, Permission: "work_item_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, actor)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{actor}, Type: e.Type, Severity: Action, TemplateKey: "notification.automation_run_failed", Permission: "work_item_read", Params: params, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/work-items/" + fact.WorkItemID.String(), DedupeKey: "automation-rule:" + fact.RuleID.String()}, fact.ID, fact.ID)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
