package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAssignmentState     = errors.New("delivery: work or assignment state does not allow this command")
	ErrAssignmentHeld      = errors.New("delivery: an executor already holds this work")
	ErrAssignmentHolder    = errors.New("delivery: only the current Human holder can change this assignment")
	ErrAssignmentReadiness = errors.New("delivery: acknowledge current readiness issues before self-claiming")
)

type assignmentCreateBody struct {
	SeenSeq                    *int64  `json:"seen_seq,omitempty" minimum:"0"`
	Role                       string  `json:"role" enum:"EXECUTOR"`
	AssigneePrincipalID        guid.ID `json:"assignee_principal_id"`
	LeaseTTLSeconds            *int    `json:"lease_ttl_seconds,omitempty" minimum:"900" maximum:"86400"`
	AcknowledgeReadinessIssues bool    `json:"acknowledge_readiness_issues,omitempty"`
}
type assignmentLeaseBody struct {
	TTLSeconds *int `json:"ttl_seconds,omitempty" minimum:"900" maximum:"86400"`
}
type AssignmentView struct {
	ID                  guid.ID    `json:"id"`
	ProjectID           guid.ID    `json:"project_id"`
	WorkItemID          guid.ID    `json:"work_item_id"`
	Role                string     `json:"role"`
	AssigneeKind        string     `json:"assignee_kind"`
	AssigneePrincipalID guid.ID    `json:"assignee_principal_id"`
	AgentID             *guid.ID   `json:"agent_id"`
	Status              string     `json:"status"`
	LeaseTTLMS          int64      `json:"lease_ttl_ms"`
	LeaseExpiresAt      *time.Time `json:"lease_expires_at"`
	LeaseRenewedAt      *time.Time `json:"lease_renewed_at"`
	EndedAt             *time.Time `json:"ended_at"`
	EndReason           *string    `json:"end_reason"`
	Version             int        `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
type assignmentReceipt struct {
	Assignment          AssignmentView `json:"assignment"`
	WorkItem            WorkView       `json:"work_item"`
	ReadinessOverrideBy *guid.ID       `json:"readiness_override_by"`
}

func assignmentRecord(row *model.Assignment) AssignmentView {
	return AssignmentView{ID: row.ID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, Role: row.Role, AssigneeKind: row.AssigneeKind, AssigneePrincipalID: row.AssigneePrincipalID, AgentID: row.AgentID, Status: row.Status, LeaseTTLMS: row.LeaseTTLMS, LeaseExpiresAt: row.LeaseExpiresAt, LeaseRenewedAt: row.LeaseRenewedAt, EndedAt: row.EndedAt, EndReason: row.EndReason, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func humanLeaseTTL(seconds *int, fallback int64) (int64, error) {
	if seconds == nil {
		if fallback >= 900000 && fallback <= 86400000 {
			return fallback, nil
		}
		return 0, ErrInvalidArgument
	}
	if *seconds < 900 || *seconds > 86400 {
		return 0, ErrInvalidArgument
	}
	return int64(*seconds) * 1000, nil
}

func (m *Module) claimHumanWorkOn(ctx context.Context, tx *gorm.DB, tenant, entity, id, actor, owner guid.ID, expected int, input assignmentCreateBody) (assignmentReceipt, error) {
	var empty assignmentReceipt
	if input.Role != "EXECUTOR" || input.AssigneePrincipalID != actor {
		return empty, ErrAssignmentHolder
	}
	ttl, err := humanLeaseTTL(input.LeaseTTLSeconds, 14400000)
	if err != nil {
		return empty, err
	}
	project, work, ownerActive, err := m.lockedWorkOn(ctx, tx, tenant, entity, id, actor, owner, "work_item_execute")
	if err != nil {
		return empty, err
	}
	if err := m.checkWorkSeenOn(ctx, tx, work, input.SeenSeq); err != nil {
		return empty, err
	}
	// The work lock serializes all ownership changes; read the slot before CAS so
	// a competing claim reports ownership contention, never creates another owner.
	q := query.BindAssignments(tx, m.service.names)
	current, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.WorkItemID.Eq(id), q.Role.Eq("EXECUTOR"), q.Status.Eq("ACTIVE"), q.EnsemblePlanID.IsNull()).First()
	if err == nil && current != nil {
		return empty, ErrAssignmentHeld
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	if work.Version != expected {
		return empty, ErrWorkVersion
	}
	if project.Stage != "ACTIVE" || work.PausedAt != nil || work.IsContainer || !slices.Contains([]string{"DRAFT", "READY"}, work.State) || work.CurrentClaimID != nil {
		return empty, ErrAssignmentState
	}
	if work.CurrentExecutionID != nil {
		return empty, ErrUnavailable
	}
	issues, blocks, err := m.evaluatePreparationOn(ctx, tx, work, ownerActive)
	if err != nil {
		return empty, err
	}
	if len(blocks) > 0 {
		return empty, ErrAssignmentState
	}
	if work.State == "DRAFT" && !input.AcknowledgeReadinessIssues {
		return empty, ErrAssignmentReadiness
	}
	if work.State == "READY" && (!work.ReadinessOK || len(issues) > 0) {
		return empty, ErrAssignmentReadiness
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	expires := now.Add(time.Duration(ttl) * time.Millisecond)
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	row := &model.Assignment{TenantID: tenant, ID: guid.ID(value), ProjectID: entity, WorkItemID: id, Role: "EXECUTOR", AssigneeKind: "HUMAN", AssigneePrincipalID: actor, Status: "ACTIVE", ActiveSlot: &id, LeaseTTLMS: ttl, LeaseExpiresAt: &expires, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
	if err = q.WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	before := work.State
	if before == "DRAFT" {
		work.ReadinessOverrideBy = &actor
		// An override records permission to proceed; it never asserts readiness.
		work.ReadinessOK = false
		encoded, _ := json.Marshal(issues)
		work.ReadinessIssues = string(encoded)
		event := audit.NewDomainEvent(ctx, tenant, "delivery.readiness_overridden", "work_item", id)
		event.Detail["assignment_id"] = row.ID.String()
		if err = m.service.audit(ctx, tx, event); err != nil {
			return empty, err
		}
	}
	work.State = "CLAIMED"
	if work.FirstClaimedAt == nil {
		work.FirstClaimedAt = &now
	}
	if err = m.assignmentWorkChangedOn(ctx, tx, work, row, actor, before, "SELF_CLAIMED", now); err != nil {
		return empty, err
	}
	return assignmentReceipt{Assignment: assignmentRecord(row), WorkItem: workRecord(work), ReadinessOverrideBy: work.ReadinessOverrideBy}, nil
}

// Both commands require current authorization and holder identity. A suspended
// lease stays NULL on renewal; reading the lease is never a permission grant.
func (m *Module) changeHumanAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, entity, workID, id, actor, owner guid.ID, expected int, renew *assignmentLeaseBody, seen *int64) (assignmentReceipt, error) {
	var empty assignmentReceipt
	_, work, ownerActive, err := m.lockedWorkOn(ctx, tx, tenant, entity, workID, actor, owner, "work_item_execute")
	if err != nil {
		return empty, err
	}
	if renew == nil {
		if err = m.checkWorkSeenOn(ctx, tx, work, seen); err != nil {
			return empty, err
		}
	}
	q := query.BindAssignments(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(entity), q.WorkItemID.Eq(workID), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.AssigneeKind != "HUMAN" || row.AssigneePrincipalID != actor {
		return empty, ErrAssignmentHolder
	}
	if row.Version != expected {
		return empty, ErrWorkVersion
	}
	if row.Status != "ACTIVE" || row.Role != "EXECUTOR" || row.EnsemblePlanID != nil {
		return empty, ErrAssignmentState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if renew != nil {
		ttl, e := humanLeaseTTL(renew.TTLSeconds, row.LeaseTTLMS)
		if e != nil {
			return empty, e
		}
		row.LeaseTTLMS = ttl
		row.LeaseRenewedAt = &now
		if row.LeaseExpiresAt != nil && work.PausedAt == nil && slices.Contains([]string{"CLAIMED", "IN_PROGRESS"}, work.State) {
			expires := now.Add(time.Duration(ttl) * time.Millisecond)
			row.LeaseExpiresAt = &expires
		} else {
			row.LeaseExpiresAt = nil
		}
		row.Version++
		row.UpdatedAt = now
		row.UpdatedBy = &actor
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected), q.Status.Eq("ACTIVE")).Select(q.LeaseTTLMS, q.LeaseExpiresAt, q.LeaseRenewedAt, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row)
		if e != nil {
			return empty, e
		}
		if result.RowsAffected != 1 {
			return empty, ErrWorkVersion
		}
		event := audit.NewDomainEvent(ctx, tenant, "delivery.lease_renewed", "assignment", id)
		event.Detail["work_item_id"] = workID.String()
		event.Detail["version"] = row.Version
		event.Detail["lease_ttl_ms"] = ttl
		if err = m.service.audit(ctx, tx, event); err != nil {
			return empty, err
		}
	} else {
		if !slices.Contains([]string{"CLAIMED", "IN_PROGRESS"}, work.State) || work.CurrentClaimID != nil {
			return empty, ErrAssignmentState
		}
		// Until execution ownership exists, a reference cannot be treated as proof
		// that no execution is active. Refuse rather than fabricate a lifecycle fact.
		if work.CurrentExecutionID != nil {
			return empty, ErrUnavailable
		}
		issues, blocks, e := m.evaluatePreparationOn(ctx, tx, work, ownerActive)
		if e != nil {
			return empty, e
		}
		row.Status = "RELEASED"
		row.ActiveSlot = nil
		row.EndedAt = &now
		reason := "RELEASED_BY_ASSIGNEE"
		row.EndReason = &reason
		row.Version++
		row.UpdatedAt = now
		row.UpdatedBy = &actor
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected), q.Status.Eq("ACTIVE")).Select(q.Status, q.ActiveSlot, q.EndedAt, q.EndReason, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(row)
		if e != nil {
			return empty, e
		}
		if result.RowsAffected != 1 {
			return empty, ErrWorkVersion
		}
		before := work.State
		work.ReadinessOK = len(issues) == 0
		encoded, _ := json.Marshal(issues)
		work.ReadinessIssues = string(encoded)
		encoded, _ = json.Marshal(blocks)
		work.BlockReasons = string(encoded)
		switch {
		case len(blocks) > 0:
			work.State = "BLOCKED"
		case !work.ReadinessOK:
			work.State = "DRAFT"
		default:
			work.State = "READY"
		}
		if err = m.assignmentWorkChangedOn(ctx, tx, work, row, actor, before, "RELEASED_BY_ASSIGNEE", now); err != nil {
			return empty, err
		}
	}
	return assignmentReceipt{Assignment: assignmentRecord(row), WorkItem: workRecord(work), ReadinessOverrideBy: work.ReadinessOverrideBy}, nil
}

func (m *Module) assignmentWorkChangedOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, assignment *model.Assignment, actor guid.ID, before, reason string, now time.Time) error {
	return m.assignmentWorkChangedByOn(ctx, tx, work, assignment, actor, "HUMAN", "permission:work_item_execute", before, reason, now)
}

func (m *Module) assignmentWorkChangedByOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, assignment *model.Assignment, actor guid.ID, kind, source, before, reason string, now time.Time) error {
	activityKind := "ASSIGNMENT_RELEASED"
	if assignment.Status == "ACTIVE" {
		activityKind = "ASSIGNED"
	}
	seq, err := m.recordWorkActivityOn(ctx, tx, work.TenantID, work.ID, WorkActivityInput{Kind: activityKind, ActorKind: kind, ActorID: actor, RefKind: "assignment", RefID: assignment.ID, Title: reason})
	if err != nil {
		return err
	}
	work.ActivitySeq = seq
	expected := work.Version
	work.Version++
	work.UpdatedAt = now
	work.UpdatedBy = nil
	if actor > 0 {
		work.UpdatedBy = &actor
	}
	work.StateChangedAt = now
	w := query.BindWorkItems(tx, m.service.names)
	result, err := w.WithContext(ctx).Where(w.TenantID.Eq(work.TenantID), w.ProjectID.Eq(work.ProjectID), w.ID.Eq(work.ID), w.Version.Eq(expected)).Select(w.State, w.FirstClaimedAt, w.ReadinessOverrideBy, w.ReadinessOK, w.ReadinessIssues, w.BlockReasons, w.StateChangedAt, w.Version, w.UpdatedAt, w.UpdatedBy).Updates(work)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrWorkVersion
	}
	value, err := m.ids.Next()
	if err != nil {
		return err
	}
	trigger := "COMMAND"
	var actorID *guid.ID
	if actor > 0 {
		actorID = &actor
	}
	if kind == "SYSTEM" {
		trigger = "SYSTEM"
	}
	transition := &model.WorkItemTransition{TenantID: work.TenantID, ID: guid.ID(value), ProjectID: work.ProjectID, WorkItemID: work.ID, FromState: &before, ToState: work.State, Trigger: trigger, ActorKind: kind, ActorID: actorID, AuthoritySource: source, ReasonCode: reason, VersionAfter: work.Version, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, work.TenantID, "delivery.work_assignment_changed", "work_item", work.ID)
	event.Detail["assignment_id"] = assignment.ID.String()
	event.Detail["from"] = before
	event.Detail["to"] = work.State
	event.Detail["reason_code"] = reason
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	out := events.Event{TenantID: work.TenantID, ProjectID: work.ProjectID, Type: "work_item.state_changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: map[string]any{"from": before, "to": work.State, "reason_code": reason, "assignment_id": assignment.ID.String()}}
	if _, err = m.service.events.Publish(ctx, tx, out); err != nil {
		return err
	}
	if work.State == "CLAIMED" {
		out.Type = "work_item.assigned"
		out.Data = map[string]any{"work_item_id": work.ID.String(), "assignment_id": assignment.ID.String(), "role": "EXECUTOR", "assignee": map[string]any{"kind": "HUMAN", "principal_id": actor.String()}, "lease_expires_at": assignment.LeaseExpiresAt}
		_, err = m.service.events.Publish(ctx, tx, out)
	}
	return err
}
