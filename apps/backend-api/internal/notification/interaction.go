package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
)

func (m *Module) projectInteractionOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	var state string
	if err := json.Unmarshal(data["approval_state"], &state); err != nil {
		return err
	}
	if state != "PENDING" {
		return nil
	}
	if m.interactionFacts == nil || m.candidates == nil {
		return ErrUnavailable
	}
	approval := rawID(data["approval_id"])
	if m.interactionReminder != nil {
		if err := m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: "interaction", SourceID: approval, Ordinal: 1}); err != nil {
			return err
		}
	}
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
			execution, project, eligible, err := m.interactionFacts(ctx, tx, e.TenantID, approval, id)
			if err != nil {
				return err
			}
			if !eligible || project != e.ProjectID || execution != e.SubjectID {
				continue
			}
			err = m.notifySourceOn(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: project, Recipients: []guid.ID{id}, Type: "interaction.requested", Severity: Action, TemplateKey: "notification.interaction_requested", Permission: "interaction_respond", Params: map[string]string{"subject_id": approval.String(), "execution_id": execution.String()}, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + project.String() + "/executions/" + execution.String(), DedupeKey: "interaction:" + execution.String()}, approval, approval)
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
func (m *Module) interactionAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, recipient guid.ID) (bool, error) {
	if m.interactionFacts == nil {
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
	execution, project, eligible, err := m.interactionFacts(ctx, tx, row.TenantID, id, recipient)
	return eligible && project == row.ProjectID && execution.String() == params["execution_id"], err
}
