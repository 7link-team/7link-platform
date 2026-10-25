package notification

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func (m *Module) exceptionAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.exceptionFacts == nil {
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
	fact, exists, err := m.exceptionFacts(ctx, tx, row.TenantID, id)
	if err != nil || !exists || fact.ProjectID != row.ProjectID || (fact.State != "RAISED" && fact.State != "REPORTED") {
		return false, err
	}
	allowed, err := m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "exception_decide")
	if err != nil || allowed {
		return allowed, err
	}
	return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
}
