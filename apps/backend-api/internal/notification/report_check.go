package notification

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"strconv"
)

func (m *Module) reportCheckedAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.planningFacts.CheckedOn == nil {
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
	version, err := strconv.Atoi(p["report_version"])
	if err != nil || version < 1 {
		return false, ErrInvalid
	}
	return m.planningFacts.CheckedOn(ctx, tx, row.TenantID, id, actor, version)
}
func (m *Module) projectReportCheckedOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "planning_report" {
		return ErrInvalid
	}
	var result string
	if json.Unmarshal(data["result"], &result) != nil {
		return ErrInvalid
	}
	if result != "BLOCKED" {
		return nil
	}
	version := int(rawID(data["report_version"]))
	if version < 1 {
		return ErrInvalid
	}
	if m.planningFacts.FactsOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	fact, found, err := m.planningFacts.FactsOn(ctx, tx, e.TenantID, e.SubjectID, "report")
	if err != nil || !found || fact.ProjectID != e.ProjectID {
		return err
	}
	params := map[string]string{"subject_id": e.SubjectID.String(), "report_version": strconv.Itoa(version)}
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
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Type: "planning.report_checked_blocked", Permission: "report_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: row.Type, Severity: Action, TemplateKey: "notification.planning_report_blocked", Permission: row.Permission, Params: params, LinkPath: fact.LinkPath, DedupeKey: "report-check:" + e.ID.String()})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}

func (m *Module) reportOverdueAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, actor guid.ID) (bool, error) {
	if m.planningFacts.EligibleOn == nil {
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
	return m.planningFacts.EligibleOn(ctx, tx, row.TenantID, id, actor, "report", "OVERDUE_AUDIENCE")
}
