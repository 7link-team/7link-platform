package notification

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
)

// Manual reminders disappear when the report is submitted or its current
// responsible recipient loses source visibility.
func (m *Module) reportReminderAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.planningFacts.FactsOn == nil || m.planningFacts.EligibleOn == nil {
		return false, ErrUnavailable
	}
	var params map[string]string
	if json.Unmarshal([]byte(row.Params), &params) != nil {
		return false, ErrInvalid
	}
	id, err := guid.RequireID(params["report_id"])
	if err != nil {
		return false, ErrInvalid
	}
	fact, found, err := m.planningFacts.FactsOn(ctx, tx, row.TenantID, id, "report")
	if err != nil || !found {
		return false, err
	}
	if fact.State == "SUBMITTED" || fact.State == "CONFIRMED" {
		return false, nil
	}
	return m.planningFacts.EligibleOn(ctx, tx, row.TenantID, id, principal, "report", fact.State)
}
