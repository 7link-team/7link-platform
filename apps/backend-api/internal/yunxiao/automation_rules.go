package yunxiao

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"encoding/json"
	"errors"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"reflect"
	"slices"
	"strings"
	"time"
)

type AutomationDefinition struct {
	Trigger struct {
		EventType  string            `json:"event_type"`
		Filters    map[string]string `json:"filters,omitempty"`
		Cron       string            `json:"cron,omitempty" maxLength:"128"`
		Timezone   string            `json:"timezone,omitempty" maxLength:"64"`
		WorkItemID guid.ID           `json:"work_item_id,omitempty"`
	} `json:"trigger"`
	Condition      json.RawMessage    `json:"condition,omitempty"`
	Actions        []AutomationAction `json:"actions" minItems:"1" maxItems:"5"`
	MaxRunsPerHour int                `json:"max_runs_per_hour" minimum:"1" maximum:"60"`
}
type AutomationAction struct {
	Kind       string            `json:"kind" enum:"TRANSITION,SET_FIELD,ASSIGN,DISPATCH_TO_AGENT,REQUEST_VERIFICATION,CREATE_LINKED_ITEM,ADD_REPORT_ENTRY,NOTIFY"`
	Parameters map[string]string `json:"parameters"`
}
type AutomationRuleInput struct {
	Name       string               `json:"name" minLength:"1" maxLength:"80"`
	Enabled    bool                 `json:"enabled"`
	Definition AutomationDefinition `json:"definition"`
}
type AutomationRuleView struct {
	ID            guid.ID               `json:"id"`
	ProjectID     guid.ID               `json:"project_id"`
	Name          string                `json:"name"`
	Version       int                   `json:"version"`
	Enabled       bool                  `json:"enabled"`
	Archived      bool                  `json:"archived"`
	Definition    *AutomationDefinition `json:"definition,omitempty"`
	ExecutionMode string                `json:"execution_mode"`
	RunAs         string                `json:"run_as"`
}

func automationRuleView(row *model.AutomationRule) (AutomationRuleView, error) {
	out := AutomationRuleView{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Version: row.Version, Enabled: row.Enabled, Archived: row.ArchivedAt != nil, ExecutionMode: "NOTIFY_REPORT_DRAFT_PLANNING_TRANSITION_AND_VERIFICATION", RunAs: "SYSTEM:automation:" + row.ID.String()}
	if row.Definition != "" {
		out.Definition = &AutomationDefinition{}
		if e := json.Unmarshal([]byte(row.Definition), out.Definition); e != nil {
			return out, ErrUnavailable
		}
	}
	return out, nil
}

var automationTriggerTypes = []string{"work_item.state_changed", "work_item.created", "work_item.assigned", "claim.submitted", "verification.completed", "internal_qa.passed", "internal_qa.failed", "review_sheet.closed", "test_run.recorded", "clientwork.test_run_recorded", "code_link.created"}

// Keep the specification trigger compatible with the test owner's established
// durable event name. Never rewrite the source event or its replay identity.
func automationTriggerMatches(trigger, source string) bool {
	return trigger == source || (trigger == "test_run.recorded" && source == "clientwork.test_run_recorded")
}

// The bounded JSON Logic subset has no calls, code evaluation or ambient data.
// Unsupported operators are rejected at configuration time.
func automationLogic(value any, data map[string]any, depth int) (any, error) {
	if depth > 12 {
		return nil, ErrInvalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	if len(object) != 1 {
		return nil, ErrInvalid
	}
	for op, arg := range object {
		if op == "var" {
			path, ok := arg.(string)
			if !ok || !strings.HasPrefix(path, "event.") || len(path) > 120 {
				return nil, ErrInvalid
			}
			var result any = data
			for _, part := range strings.Split(path, ".") {
				entry, ok := result.(map[string]any)
				if !ok {
					return nil, nil
				}
				result = entry[part]
			}
			return result, nil
		}
		args, ok := arg.([]any)
		if !ok || len(args) > 20 {
			return nil, ErrInvalid
		}
		switch op {
		case "==", "!=":
			if len(args) != 2 {
				return nil, ErrInvalid
			}
		case "!":
			if len(args) != 1 {
				return nil, ErrInvalid
			}
		case "and", "or":
			if len(args) == 0 {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
		values := make([]any, len(args))
		for i, a := range args {
			v, e := automationLogic(a, data, depth+1)
			if e != nil {
				return nil, e
			}
			values[i] = v
		}
		switch op {
		case "==":
			return reflect.DeepEqual(values[0], values[1]), nil
		case "!=":
			return !reflect.DeepEqual(values[0], values[1]), nil
		case "!":
			return !automationTruth(values[0]), nil
		case "and":
			for _, v := range values {
				if !automationTruth(v) {
					return false, nil
				}
			}
			return true, nil
		case "or":
			for _, v := range values {
				if automationTruth(v) {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return nil, ErrInvalid
}
func automationTruth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return true
	}
}
func validateAutomationDefinition(def AutomationDefinition) (string, error) {
	if (!slices.Contains(automationTriggerTypes, def.Trigger.EventType) && def.Trigger.EventType != "schedule") || len(def.Trigger.Filters) > 10 || len(def.Actions) < 1 || len(def.Actions) > 5 || def.MaxRunsPerHour < 1 || def.MaxRunsPerHour > 60 {
		return "", ErrInvalid
	}
	if def.Trigger.EventType == "schedule" {
		if len(def.Trigger.Cron) > 128 || len(strings.Fields(def.Trigger.Cron)) != 5 || def.Trigger.WorkItemID <= 0 || def.Trigger.Timezone == "" || def.Trigger.Timezone == "Local" || len(def.Trigger.Timezone) > 64 {
			return "", ErrInvalid
		}
		if _, e := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(def.Trigger.Cron); e != nil {
			return "", ErrInvalid
		}
		if _, e := time.LoadLocation(def.Trigger.Timezone); e != nil {
			return "", ErrInvalid
		}
	} else if def.Trigger.Cron != "" || def.Trigger.Timezone != "" || def.Trigger.WorkItemID != 0 {
		return "", ErrInvalid
	}
	for key, value := range def.Trigger.Filters {
		if key == "" || len(key) > 80 || len(value) > 200 || strings.ContainsAny(key, ".[]") {
			return "", ErrInvalid
		}
	}
	if len(def.Condition) > 0 {
		var value any
		if json.Unmarshal(def.Condition, &value) != nil {
			return "", ErrInvalid
		}
		if _, e := automationLogic(value, map[string]any{}, 0); e != nil {
			return "", e
		}
	}
	for _, action := range def.Actions {
		if !slices.Contains([]string{"TRANSITION", "SET_FIELD", "ASSIGN", "DISPATCH_TO_AGENT", "REQUEST_VERIFICATION", "CREATE_LINKED_ITEM", "ADD_REPORT_ENTRY", "NOTIFY"}, action.Kind) || len(action.Parameters) == 0 || len(action.Parameters) > 8 {
			return "", ErrInvalid
		}
		for key, value := range action.Parameters {
			if key == "" || len(key) > 80 || len(value) > 300 {
				return "", ErrInvalid
			}
		}
	}
	encoded, e := json.Marshal(def)
	if e != nil || len(encoded) > 6000 {
		return "", ErrInvalid
	}
	return string(encoded), nil
}
func (m *Module) saveAutomationDefinitionOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, in AutomationRuleInput) (AutomationRuleView, error) {
	var empty AutomationRuleView
	encoded, e := validateAutomationDefinition(in.Definition)
	if e != nil {
		return empty, e
	}
	if id > 0 {
		q := query.BindAutomationRules(tx, m.names)
		old, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
		if e != nil {
			return empty, e
		}
		if old.ArchivedAt != nil {
			return empty, ErrConflict
		}
	}
	id, e = m.registerAutomationRuleOn(ctx, tx, tenant, project, id, version, in.Name, in.Enabled)
	if e != nil {
		return empty, e
	}
	if e = tx.WithContext(ctx).Table(m.names.AutomationRules.String()).Where("tenant_id = ? AND project_id = ? AND id = ?", tenant, project, id).Update("definition", encoded).Error; e != nil {
		return empty, e
	}
	q := query.BindAutomationRules(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if e != nil {
		return empty, e
	}
	return automationRuleView(row)
}

type AutomationPreview struct {
	RuleID        guid.ID            `json:"rule_id"`
	EventID       guid.ID            `json:"event_id"`
	WorkItemID    guid.ID            `json:"work_item_id"`
	Matches       bool               `json:"matches"`
	Reason        string             `json:"reason"`
	Actions       []AutomationAction `json:"actions"`
	Executed      bool               `json:"executed"`
	ExecutionMode string             `json:"execution_mode"`
}

func (m *Module) previewAutomationOn(ctx context.Context, tx *gorm.DB, tenant, project, id, event guid.ID) (AutomationPreview, error) {
	out := AutomationPreview{RuleID: id, EventID: event, Actions: []AutomationAction{}, ExecutionMode: "NOTIFY_REPORT_DRAFT_PLANNING_TRANSITION_AND_VERIFICATION"}
	if e := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); e != nil {
		return out, e
	}
	if m.automationEvents == nil || m.workItemOn == nil || event <= 0 {
		return out, ErrUnavailable
	}
	q := query.BindAutomationRules(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	source, e := m.automationEvents(ctx, tx, event-1, 1)
	if e != nil {
		return out, e
	}
	if len(source) != 1 || source[0].ID != event || source[0].TenantID != tenant || source[0].ProjectID != project {
		return out, ErrNotFound
	}
	workID, e := m.automationSourceWorkOn(ctx, tx, source[0])
	if e != nil {
		return out, e
	}
	if workID <= 0 {
		return out, ErrNotFound
	}
	if e = m.workItemOn(ctx, tx, tenant, project, workID); e != nil {
		return out, e
	}
	out.WorkItemID = workID
	matches, reason, actions, e := automationDefinitionMatches(row, source[0])
	out.Matches, out.Reason, out.Actions = matches, reason, actions
	return out, e
}
func automationDefinitionMatches(row *model.AutomationRule, event events.RecordedEvent) (bool, string, []AutomationAction, error) {
	none := []AutomationAction{}
	if !row.Enabled || row.ArchivedAt != nil {
		return false, "DISABLED", none, nil
	}
	if row.Definition == "" {
		return false, "UNCONFIGURED", none, nil
	}
	var def AutomationDefinition
	if json.Unmarshal([]byte(row.Definition), &def) != nil {
		return false, "", none, ErrUnavailable
	}
	if !automationTriggerMatches(def.Trigger.EventType, event.Type) {
		return false, "TRIGGER_MISMATCH", none, nil
	}
	payload := map[string]any{}
	if len(event.Data) > 0 && json.Unmarshal(event.Data, &payload) != nil {
		return false, "", none, ErrUnavailable
	}
	for key, wanted := range def.Trigger.Filters {
		if payload[key] != wanted {
			return false, "FILTER_MISMATCH", none, nil
		}
	}
	if len(def.Condition) > 0 {
		var condition any
		if json.Unmarshal(def.Condition, &condition) != nil {
			return false, "", none, ErrUnavailable
		}
		match, e := automationLogic(condition, map[string]any{"event": map[string]any{"type": event.Type, "data": payload}}, 0)
		if e != nil {
			return false, "", none, e
		}
		if !automationTruth(match) {
			return false, "CONDITION_FALSE", none, nil
		}
	}
	return true, "MATCHED", def.Actions, nil
}
