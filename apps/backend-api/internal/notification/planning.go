package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// The source owner binds current facts and current decision visibility without
// reversing its existing dependency on Notification.
type PlanningNotificationFact struct {
	NeedsRecheck    bool
	ProjectID       guid.ID
	State, LinkPath string
	DayKey          string
	ReminderDueAt   *time.Time
}
type PlanningNotifications struct {
	CheckedOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) (bool, error)
	LoopEligibleOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error)
	FactsOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (PlanningNotificationFact, bool, error)
	EligibleOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, string) (bool, error)
}
type PlanningNotificationConnector struct {
	Bind func(PlanningNotifications) error
}

func (m *Module) providePlanningNotifications(s *module.Services) error {
	return module.Provide[PlanningNotificationConnector](s, PlanningNotificationConnector{Bind: func(source PlanningNotifications) error {
		if source.FactsOn == nil || source.EligibleOn == nil || m.planningFacts.FactsOn != nil {
			return ErrInvalid
		}
		m.planningFacts = source
		return nil
	}})
}

func planningNotificationType(e events.RecordedEvent, d map[string]json.RawMessage) (string, string, string, bool) {
	var kind, action string
	_ = json.Unmarshal(d["kind"], &kind)
	_ = json.Unmarshal(d["action"], &action)
	if e.Type == "planning.report_generated" {
		return "report", "DRAFT", "planning.report_generated", true
	}
	if e.Type != "planning.changed" || (kind != "plan" && kind != "report" && kind != "goal") {
		return "", "", "", false
	}
	if kind == "goal" && action == "REFRESH" {
		var state, from string
		_ = json.Unmarshal(d["state"], &state)
		_ = json.Unmarshal(d["from_state"], &from)
		if state == "AT_RISK" && from != "AT_RISK" {
			return "goal", "AT_RISK", "planning.goal_at_risk", true
		}
		return "", "", "", false
	}
	state := ""
	switch action {
	case "RECHECK":
		if kind == "report" {
			return kind, "SUBMITTED", "planning.report_needs_recheck", true
		}
		return "", "", "", false
	case "SUBMIT":
		state = "SUBMITTED"
	case "CONFIRM":
		state = "CONFIRMED"
	case "RETURN":
		state = "RETURNED"
	default:
		return "", "", "", false
	}
	return kind, state, "planning." + kind + "_" + strings.ToLower(state), true
}

func (m *Module) projectPlanningEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent, data map[string]json.RawMessage) error {
	if e.ID <= 0 || e.TenantID <= 0 || e.SubjectID <= 0 {
		return ErrInvalid
	}
	kind, state, typ, ok := planningNotificationType(e, data)
	if !ok {
		return nil
	}
	if m.planningFacts.FactsOn == nil || m.planningFacts.EligibleOn == nil || m.candidates == nil {
		return ErrUnavailable
	}
	fact, found, err := m.planningFacts.FactsOn(ctx, tx, e.TenantID, e.SubjectID, kind)
	if err != nil {
		return err
	}
	if !found || fact.ProjectID != e.ProjectID || fact.State != state {
		return nil
	}
	permission := kind + "_read"
	if kind == "plan" {
		permission = "work_item_read"
	}
	severity := Info
	if state == "SUBMITTED" || state == "BLOCKED" {
		severity = Action
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
			audienceState := state
			if typ == "planning.report_needs_recheck" {
				audienceState = "NEEDS_RECHECK"
			}
			eligible, err := m.planningFacts.EligibleOn(ctx, tx, e.TenantID, e.SubjectID, id, kind, audienceState)
			if err != nil {
				return err
			}
			if !eligible {
				continue
			}
			dedupe := "planning:" + e.ID.String() + ":" + typ
			if typ == "planning.report_needs_recheck" {
				dedupe = "planning:recheck:" + e.SubjectID.String() + ":" + m.now().UTC().Format("2006-01-02")
			}
			if typ == "planning.goal_at_risk" {
				dedupe = "planning:goalrisk:" + e.SubjectID.String() + ":" + fact.DayKey
			}
			_, err = m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: []guid.ID{id}, Type: typ, Severity: severity, TemplateKey: "notification." + strings.ReplaceAll(typ, ".", "_"), Permission: permission, LinkPath: fact.LinkPath, DedupeKey: dedupe, Params: map[string]string{"subject_id": e.SubjectID.String()}})
			if err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}

func (m *Module) planningAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if m.planningFacts.FactsOn == nil || m.planningFacts.EligibleOn == nil {
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
	suffix := strings.TrimPrefix(row.Type, "planning.")
	kind, state, ok := strings.Cut(suffix, "_")
	if !ok {
		return false, ErrInvalid
	}
	state = strings.ToUpper(state)
	if state == "NEEDS_RECHECK" {
		fact, found, err := m.planningFacts.FactsOn(ctx, tx, row.TenantID, id, kind)
		if err != nil || !found || !fact.NeedsRecheck || fact.State != "SUBMITTED" || fact.ProjectID != row.ProjectID || fact.LinkPath != row.LinkPath {
			return false, err
		}
		return m.planningFacts.EligibleOn(ctx, tx, row.TenantID, id, principal, kind, state)
	}
	if state == "UNCONFIRMED" || state == "CLOSE_OVERDUE" {
		fact, found, err := m.planningFacts.FactsOn(ctx, tx, row.TenantID, id, kind)
		if err != nil || !found || fact.ProjectID != row.ProjectID || fact.LinkPath != row.LinkPath || fact.ReminderDueAt == nil || m.now().Before(*fact.ReminderDueAt) {
			return false, err
		}
		if state == "UNCONFIRMED" && fact.State != "SUBMITTED" {
			return false, nil
		}
		if state == "CLOSE_OVERDUE" && fact.State != "ACTIVE" && fact.State != "AT_RISK" {
			return false, nil
		}
		return m.planningFacts.EligibleOn(ctx, tx, row.TenantID, id, principal, kind, fact.State)
	}
	if state == "GENERATED" {
		state = "DRAFT"
	}
	fact, found, err := m.planningFacts.FactsOn(ctx, tx, row.TenantID, id, kind)
	if err != nil || !found || fact.ProjectID != row.ProjectID || fact.State != state || fact.LinkPath != row.LinkPath {
		return false, err
	}
	yes, err := m.planningFacts.EligibleOn(ctx, tx, row.TenantID, id, principal, kind, state)
	if errors.Is(err, ErrForbidden) {
		return false, nil
	}
	return yes, err
}
