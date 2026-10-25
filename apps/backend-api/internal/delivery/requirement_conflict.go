package delivery

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type requirementConflictBody struct {
	Version int    `json:"version" minimum:"1"`
	Action  string `json:"action" enum:"RAISE,RESOLVE"`
	Reason  string `json:"reason" minLength:"10" maxLength:"2000"`
}

// Requirement findings remain DRAFT until a Human explicitly resolves them and
// ordinary preparation checks pass. A resolution never contacts the client.
func (m *Module) requirementConflictOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, in requirementConflictBody) (WorkView, error) {
	var out WorkView
	reason := strings.TrimSpace(in.Reason)
	if in.Version < 1 || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 10 || utf8.RuneCountInString(reason) > 2000 || !slices.Contains([]string{"RAISE", "RESOLVE"}, in.Action) {
		return out, ErrInvalidArgument
	}
	permission := "work_item_write"
	if in.Action == "RESOLVE" {
		permission = "scope_change"
	}
	_, work, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, permission, in.Action == "RAISE")
	if err != nil {
		return out, err
	}
	if work.Version != in.Version {
		return out, ErrWorkVersion
	}
	if work.State != "DRAFT" || work.CurrentExecutionID != nil {
		return out, ErrResultState
	}
	if work.EscalationReason != nil && *work.EscalationReason != "REQUIREMENT_CONFLICT" {
		return out, ErrResultState
	}
	var issues []workReadinessIssue
	if err = json.Unmarshal([]byte(work.ReadinessIssues), &issues); err != nil {
		return out, ErrUnavailable
	}
	present := slices.ContainsFunc(issues, func(issue workReadinessIssue) bool { return issue.Code == "REQUIREMENT_CONFLICT" })
	if in.Action == "RAISE" {
		if present {
			return out, ErrResultState
		}
		issues = append(issues, workReadinessIssue{Code: "REQUIREMENT_CONFLICT", Field: "requirement", MessageKey: "delivery.readiness_requirement_conflict"})
		code := "REQUIREMENT_CONFLICT"
		work.EscalationReason = &code
	} else {
		if !present || work.EscalationReason == nil {
			return out, ErrResultState
		}
		issues = slices.DeleteFunc(issues, func(issue workReadinessIssue) bool { return issue.Code == "REQUIREMENT_CONFLICT" })
		// Other force-Human policies are not cleared by resolving a requirement.
		work.EscalationReason = nil
	}
	work.ReadinessOK = false
	raw, err := json.Marshal(issues)
	if err != nil {
		return out, err
	}
	work.ReadinessIssues = string(raw)
	at := time.Now().UTC().Truncate(time.Millisecond)
	kind, err := m.resultActorKindOn(ctx, tx, actor, "")
	if err != nil {
		return out, err
	}
	source := "permission:" + permission
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "REQUIREMENT_CONFLICT_"+in.Action, source, 0, at); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.requirement_conflict_changed", "work_item", id)
	event.Detail["action"], event.Detail["reason"], event.Detail["actor_kind"] = in.Action, reason, kind
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	typ := "work_item.requirement_conflict_resolved"
	if in.Action == "RAISE" {
		typ = "work_item.escalated"
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: typ, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(work.Version), Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: source}, Data: map[string]any{"reason_code": "REQUIREMENT_CONFLICT", "action": in.Action, "source_version": work.Version}})
	if err != nil {
		return out, err
	}
	return workRecord(work), nil
}
