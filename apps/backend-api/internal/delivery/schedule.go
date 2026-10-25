package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
	"strings"
	"time"
)

// Schedule timestamps are UTC instants; timezone preserves the intended calendar
// interpretation. A null pair is explicitly unscheduled, not a fabricated date.
type scheduleBody struct {
	Version  int        `json:"version" minimum:"1"`
	Start    *time.Time `json:"start"`
	End      *time.Time `json:"end"`
	Timezone string     `json:"timezone" minLength:"1" maxLength:"80"`
	Reason   string     `json:"reason" minLength:"5" maxLength:"2000"`
}
type calendarRow struct {
	Work         WorkView `json:"work"`
	AssigneeKind string   `json:"assignee_kind"`
	AssigneeID   *guid.ID `json:"assignee_id"`
}

func (m *Module) scheduleWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, in scheduleBody) (WorkView, error) {
	var empty WorkView
	reason, err := resultText(in.Reason)
	if err != nil || len([]rune(reason)) < 5 {
		return empty, ErrInvalidArgument
	}
	if len(in.Timezone) > 80 || strings.TrimSpace(in.Timezone) == "" {
		return empty, ErrInvalidArgument
	}
	if _, err = time.LoadLocation(in.Timezone); err != nil {
		return empty, ErrInvalidArgument
	}
	if (in.Start == nil) != (in.End == nil) {
		return empty, ErrInvalidArgument
	}
	if in.Start != nil && (!in.End.After(*in.Start) || in.Start.Year() < 1 || in.End.Year() > 9999) {
		return empty, ErrInvalidArgument
	}
	_, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
	if err != nil {
		return empty, err
	}
	if work.Version != in.Version {
		return empty, ErrWorkVersion
	}
	if work.State == "ACCEPTED" || work.State == "CANCELLED" { //nolint:misspell // Frozen persisted protocol state.
		return empty, ErrWorkPreparationState
	}
	beforeStart, beforeEnd := work.ScheduledStart, work.ScheduledEnd
	if in.Start != nil {
		start, end := in.Start.UTC().Truncate(time.Millisecond), in.End.UTC().Truncate(time.Millisecond)
		in.Start, in.End = &start, &end
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	changed := tx.WithContext(ctx).Table(m.service.names.WorkItems.String()).Where("tenant_id = ? AND project_id = ? AND id = ? AND version = ?", tenant, project, id, in.Version).Updates(map[string]any{"scheduled_start": in.Start, "scheduled_end": in.End, "schedule_timezone": in.Timezone, "version": in.Version + 1, "updated_at": now, "updated_by": actor})
	if changed.Error != nil {
		return empty, changed.Error
	}
	if changed.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	work.ScheduledStart, work.ScheduledEnd, work.ScheduleTimezone = in.Start, in.End, in.Timezone
	work.Version++
	work.UpdatedAt = now
	work.UpdatedBy = &actor
	event := audit.NewDomainEvent(ctx, tenant, "delivery.work_schedule_changed", "work_item", id)
	event.Detail["reason"] = reason
	event.Detail["before_start"] = beforeStart
	event.Detail["before_end"] = beforeEnd
	event.Detail["start"] = in.Start
	event.Detail["end"] = in.End
	event.Detail["timezone"] = in.Timezone
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.schedule_changed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:work_item_write"}, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(work.Version), Data: map[string]any{"start": in.Start, "end": in.End, "timezone": in.Timezone, "reason": reason}})
	return workRecord(work), err
}
func (m *Module) calendarRowsOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, in *calendarInput) ([]calendarRow, int64, error) {
	rows := []calendarRow{}
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
		return nil, 0, ErrInvalidArgument
	}
	if in.AssigneeKind != "" && in.AssigneeKind != "HUMAN" && in.AssigneeKind != "AGENT" {
		return nil, 0, ErrInvalidArgument
	}
	policy, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
	if err != nil {
		return nil, 0, err
	}
	q := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(policy, project, q.CreatedBy)
	if err != nil {
		return nil, 0, err
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	// The range is an overlap query: tasks spanning the entire visible interval
	// remain visible. Unscheduled is a distinct query and never falls on today.
	if in.Unscheduled {
		base = base.Where(q.ScheduledStart.IsNull())
	} else {
		if in.Start.IsZero() || !in.End.After(in.Start) || in.End.Sub(in.Start) > 370*24*time.Hour {
			return nil, 0, ErrInvalidArgument
		}
		base = base.Where(q.ScheduledStart.Lt(in.End), q.ScheduledEnd.Gt(in.Start))
	}
	if in.AssigneeKind != "" || in.AssigneeID != 0 {
		a := query.BindAssignments(tx, m.service.names)
		active := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.Status.Eq("ACTIVE"), a.Role.Eq("EXECUTOR"))
		if in.AssigneeKind != "" {
			active = active.Where(a.AssigneeKind.Eq(in.AssigneeKind))
		}
		if in.AssigneeID != 0 {
			if in.AssigneeID < 0 {
				return nil, 0, ErrInvalidArgument
			}
			active = active.Where(a.AssigneePrincipalID.Eq(in.AssigneeID))
		}
		assignments, e := active.Find()
		if e != nil {
			return nil, 0, e
		}
		if len(assignments) == 0 {
			return rows, 0, nil
		}
		ids := []guid.ID{}
		for _, a := range assignments {
			ids = append(ids, a.WorkItemID)
		}
		base = base.Where(q.ID.In(idValues(ids)...))
	}
	total, err := base.Count()
	if err != nil {
		return nil, 0, err
	}
	work, err := base.Order(q.ScheduledStart, q.ID).Offset(in.Offset).Limit(in.Limit).Find()
	if err != nil {
		return nil, 0, err
	}
	for _, item := range work {
		row := calendarRow{Work: workRecord(item)}
		a := query.BindAssignments(tx, m.service.names)
		assigned, e := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(item.ID), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE")).Order(a.ID).Find()
		if e != nil {
			return nil, 0, e
		}
		if len(assigned) > 0 {
			row.AssigneeKind = assigned[0].AssigneeKind
			row.AssigneeID = &assigned[0].AssigneePrincipalID
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}
