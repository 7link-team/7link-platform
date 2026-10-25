package notification

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

func decisionRoute(e events.RecordedEvent, data map[string]json.RawMessage) (route, bool) {
	switch e.Type {
	case "plan.proposed":
		return route{e.Type, "work_item_read", Action, true, ""}, true
	case "plan.approved", "plan.rejected":
		return route{e.Type, "work_item_read", Info, true, ""}, true
	case "work_item.escalated":
		var reason, trigger string
		_ = json.Unmarshal(data["reason_code"], &reason)
		_ = json.Unmarshal(data["trigger"], &trigger)
		if reason == "MANDATORY_HUMAN_GATE" {
			return route{"verification.human_requested", "work_item_write", Action, true, ""}, true
		}
		// Explicit escalations qualify; ordinary internal failures do not.
		if reason != "" || trigger != "" {
			return route{e.Type, "work_item_read", Action, true, ""}, true
		}
	}
	return route{}, false
}

func (m *Module) decisionAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if row.Type == "project.constitution_submitted" {
		if m.decisionFacts.ConstitutionEligibleOn == nil {
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
		project, yes, err := m.decisionFacts.ConstitutionEligibleOn(ctx, tx, row.TenantID, id, principal)
		return yes && project == row.ProjectID, err
	}

	if m.decisionFacts.FactsOn == nil {
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
	kind := "work_item"
	if strings.HasPrefix(row.Type, "plan.") {
		kind = "plan"
	}
	fact, exists, err := m.decisionFacts.FactsOn(ctx, tx, row.TenantID, id, kind)
	if err != nil || !exists || fact.ProjectID != row.ProjectID {
		return false, err
	}
	if row.Type == "plan.proposed" {
		if m.decisionFacts.PlanEligibleOn == nil {
			return false, ErrUnavailable
		}
		return m.decisionFacts.PlanEligibleOn(ctx, tx, row.TenantID, id, principal)
	}
	if kind == "plan" {
		return fact.AuthorID == principal, nil
	}
	if row.Type == "work_item.escalated" && params["reason_code"] == "REQUIREMENT_CONFLICT" {
		if fact.State != "DRAFT" || fact.EscalationReason != "REQUIREMENT_CONFLICT" || strconv.Itoa(fact.ConflictVersion) != params["source_version"] {
			return false, nil
		}
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	}
	if fact.AccountableID == principal {
		return true, nil
	}
	return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
}

func (m *Module) projectConstitutionOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.SubjectID <= 0 || e.SubjectKind != "project_constitution" {
		return ErrInvalid
	}
	if m.candidates == nil {
		return ErrUnavailable
	}
	p := map[string]string{"subject_id": e.SubjectID.String()}
	raw, err := json.Marshal(p)
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
			row := &model.Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Type: e.Type, Permission: "project_read", Params: string(raw)}
			yes, err := m.visibleOn(ctx, tx, row, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: e.Type, Severity: Action, Permission: "project_read", TemplateKey: "notification.project_constitution_submitted", Params: p, LinkPath: "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/project?view=constitution", DedupeKey: e.Type + ":" + e.SubjectID.String()})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
