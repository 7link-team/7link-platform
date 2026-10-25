package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) bindAutomationNotifier(s *module.Services) error {
	connector, err := module.Resolve[yunxiao.AutomationNotifierConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	return connector.Bind(m.automationNotifyOn)
}
func (m *Module) automationNotifyOn(ctx context.Context, tx *gorm.DB, source yunxiao.AutomationNotice) error {
	if !validTransaction(tx) || source.TenantID <= 0 || source.ProjectID <= 0 || source.RuleID <= 0 || source.RunID <= 0 || source.WorkItemID <= 0 || source.RecipientID <= 0 || source.RunAs != "SYSTEM:automation:"+source.RuleID.String() {
		return ErrInvalid
	}
	params := map[string]string{"rule_name": source.RuleName, "rule_id": source.RuleID.String(), "run_id": source.RunID.String(), "work_item_id": source.WorkItemID.String()}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	visible, err := m.visibleOn(ctx, tx, &model.Notification{TenantID: source.TenantID, ProjectID: source.ProjectID, RecipientPrincipalID: source.RecipientID, Type: "automation.notice", Permission: "work_item_read", Params: string(encoded)}, source.RecipientID)
	if err != nil {
		return err
	}
	if !visible {
		return ErrForbidden
	}
	ids, err := m.Notify(ctx, tx, Notification{TenantID: source.TenantID, ProjectID: source.ProjectID, Recipients: []guid.ID{source.RecipientID}, Type: "automation.notice", Severity: Info, TemplateKey: "notification.automation_notice", Permission: "work_item_read", Params: params, LinkPath: "/admin/" + source.TenantID.String() + "/entities/" + source.ProjectID.String() + "/work-items/" + source.WorkItemID.String(), DedupeKey: "automation-run:" + source.RunID.String()})
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return ErrForbidden
	}
	return nil
}
