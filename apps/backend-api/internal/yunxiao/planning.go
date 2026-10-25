package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrVersionLocked = errors.New("yunxiao: planning version locked")
var ErrIterationClosed = errors.New("yunxiao: iteration closed")

type PlanningEntityInput struct {
	Name             string     `json:"name" minLength:"1" maxLength:"120"`
	Goal             string     `json:"goal,omitempty" maxLength:"500"`
	GoalID           *guid.ID   `json:"goal_id,omitempty"`
	StartsAt         *time.Time `json:"starts_at,omitempty"`
	EndsAt           *time.Time `json:"ends_at,omitempty"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	PlannedReleaseAt *time.Time `json:"planned_release_at,omitempty"`
}
type PlanningEntity struct {
	ID                  guid.ID `json:"id"`
	ProjectID           guid.ID `json:"project_id"`
	PlanningEntityInput `gorm:"embedded"`
	State               string     `json:"state"`
	ClosedAt            *time.Time `json:"closed_at,omitempty"`
	ReleasedAt          *time.Time `json:"released_at,omitempty"`
	LockedAt            *time.Time `json:"locked_at,omitempty"`
	LockedBy            *guid.ID   `json:"locked_by,omitempty"`
	AchievedAt          *time.Time `json:"achieved_at,omitempty"`
	CreatedBy           guid.ID    `json:"created_by"`
	Version             int        `json:"version"`
}
type PlanningAssignmentInput struct {
	IterationID *guid.ID `json:"iteration_id"`
	VersionID   *guid.ID `json:"version_id"`
	MilestoneID *guid.ID `json:"milestone_id"`
}
type PlanningAssignment struct {
	WorkItemID guid.ID `json:"work_item_id"`
	PlanningAssignmentInput
	Version int `json:"version"`
}

func planningKind(kind string) bool {
	return slices.Contains([]string{"iterations", "versions", "milestones"}, kind)
}

// The three resource families share optimistic version and scope semantics.
// All table identities are resolver-produced objects, never constructed here.
func (m *Module) planningStore(tx *gorm.DB, kind string) *gorm.DB {
	switch kind {
	case "iterations":
		return tx.Table(m.names.Iterations.String())
	case "versions":
		return tx.Table(m.names.Versions.String())
	case "milestones":
		return tx.Table(m.names.Milestones.String())
	}
	return nil
}
func (m *Module) planningEntityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind string) (PlanningEntity, error) {
	var out PlanningEntity
	if !planningKind(kind) || id <= 0 {
		return out, ErrInvalid
	}
	err := m.planningStore(tx.WithContext(ctx), kind).Where(map[string]any{"tenant_id": tenant, "project_id": project, "id": id}).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return out, err
}
func (m *Module) savePlanningEntityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind string, version int, in PlanningEntityInput) (PlanningEntity, error) {
	var out PlanningEntity
	if err := m.projectAuthorityOn(ctx, tx, tenant, project, "workflow_manage"); err != nil {
		return out, err
	}
	if !planningKind(kind) || strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 120 || utf8.RuneCountInString(in.Goal) > 500 || (in.GoalID != nil && *in.GoalID <= 0) {
		return out, ErrInvalid
	}
	values := map[string]any{"name": strings.TrimSpace(in.Name), "updated_at": m.now()}
	switch kind {
	case "iterations":
		if utf8.RuneCountInString(in.Name) > 80 || in.StartsAt == nil || in.EndsAt == nil || in.EndsAt.Sub(*in.StartsAt) < 24*time.Hour || in.EndsAt.Sub(*in.StartsAt) > 90*24*time.Hour {
			return out, ErrInvalid
		}
		values["starts_at"], values["ends_at"], values["goal"], values["goal_id"] = in.StartsAt, in.EndsAt, in.Goal, in.GoalID
	case "versions":
		if utf8.RuneCountInString(in.Name) > 80 {
			return out, ErrInvalid
		}
		values["planned_release_at"] = in.PlannedReleaseAt
	case "milestones":
		if in.DueAt == nil {
			return out, ErrInvalid
		}
		values["due_at"], values["goal_id"] = in.DueAt, in.GoalID
	}
	if id == 0 {
		if version != 0 {
			return out, ErrConflict
		}
		value, err := m.ids.Next()
		if err != nil {
			return out, err
		}
		if value <= 0 {
			return out, ErrUnavailable
		}
		id = guid.ID(value)
		values["id"], values["tenant_id"], values["project_id"], values["version"], values["created_at"], values["created_by"] = id, tenant, project, 1, m.now(), authn.PrincipalIDFromContext(ctx)
		if kind == "iterations" {
			values["state"] = "PLANNED"
		}
		if kind == "versions" {
			values["state"] = "PLANNING"
		}
		if err := m.planningStore(tx.WithContext(ctx), kind).Create(values).Error; err != nil {
			return out, err
		}
	} else {
		current, err := m.planningEntityOn(ctx, tx, tenant, project, id, kind)
		if err != nil {
			return out, err
		}
		if current.Version != version {
			return out, ErrConflict
		}
		if current.State == "CLOSED" {
			return out, ErrIterationClosed
		}
		if current.State == "LOCKED" {
			return out, ErrVersionLocked
		}
		values["version"] = version + 1
		result := m.planningStore(tx.WithContext(ctx), kind).Where(map[string]any{"tenant_id": tenant, "project_id": project, "id": id, "version": version}).Updates(values)
		if result.Error != nil {
			return out, result.Error
		}
		if result.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	out, err := m.planningEntityOn(ctx, tx, tenant, project, id, kind)
	if err != nil {
		return out, err
	}
	return out, m.workChangedOn(ctx, tx, tenant, project, id, out.Version, "planning.entity_changed", false, kind)
}
func assignmentView(row *model.WorkPlanning) PlanningAssignment {
	return PlanningAssignment{WorkItemID: row.WorkItemID, PlanningAssignmentInput: PlanningAssignmentInput{IterationID: row.IterationID, VersionID: row.VersionID, MilestoneID: row.MilestoneID}, Version: row.Version}
}
func (m *Module) assignmentOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (*model.WorkPlanning, error) {
	q := query.BindWorkPlanning(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return row, err
}
func equalPlanningID(a, b *guid.ID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func (m *Module) assignPlanningOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, version int, in PlanningAssignmentInput) (PlanningAssignment, error) {
	return m.assignPlanningActorOn(ctx, ctx, tx, tenant, project, work, version, in, nil)
}

func (m *Module) assignPlanningActorOn(ctx, authorityCtx context.Context, tx *gorm.DB, tenant, project, work guid.ID, version int, in PlanningAssignmentInput, automation *AutomationPlanningChange) (PlanningAssignment, error) {
	var out PlanningAssignment
	if err := m.projectAuthorityOn(authorityCtx, tx, tenant, project, "work_item_write"); err != nil {
		return out, err
	}
	if m.planningWork == nil {
		return out, ErrUnavailable
	}
	if _, err := m.planningWork(authorityCtx, tx, tenant, project, work); err != nil {
		return out, err
	}
	row, err := m.assignmentOn(ctx, tx, tenant, project, work)
	if err != nil {
		return out, err
	}
	if row == nil && version != 0 || row != nil && row.Version != version {
		return out, ErrConflict
	}
	if row != nil && row.VersionID != nil && !equalPlanningID(row.VersionID, in.VersionID) {
		old, err := m.planningEntityOn(ctx, tx, tenant, project, *row.VersionID, "versions")
		if err != nil {
			return out, err
		}
		if old.State == "LOCKED" {
			return out, ErrVersionLocked
		}
	}
	for kind, id := range map[string]*guid.ID{"iterations": in.IterationID, "versions": in.VersionID, "milestones": in.MilestoneID} {
		if id == nil {
			continue
		}
		target, err := m.planningEntityOn(ctx, tx, tenant, project, *id, kind)
		if err != nil {
			return out, err
		}
		if target.State == "CLOSED" {
			return out, ErrIterationClosed
		}
		if kind == "versions" && target.State == "LOCKED" && (row == nil || !equalPlanningID(row.VersionID, id)) {
			return out, ErrVersionLocked
		}
	}
	q := query.BindWorkPlanning(tx, m.names)
	if row == nil {
		value, err := m.ids.Next()
		if err != nil {
			return out, err
		}
		if value <= 0 {
			return out, ErrUnavailable
		}
		row = &model.WorkPlanning{ID: guid.ID(value), TenantID: tenant, ProjectID: project, WorkItemID: work, Version: 1, UpdatedBy: authn.PrincipalIDFromContext(ctx), UpdatedAt: m.now(), IterationID: in.IterationID, VersionID: in.VersionID, MilestoneID: in.MilestoneID}
		if err = q.WithContext(ctx).Create(row); err != nil {
			return out, err
		}
	} else {
		info, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.Version.Eq(version)).Updates(map[string]any{"iteration_id": in.IterationID, "version_id": in.VersionID, "milestone_id": in.MilestoneID, "version": version + 1, "updated_by": authn.PrincipalIDFromContext(ctx), "updated_at": m.now()})
		if err != nil {
			return out, err
		}
		if info.RowsAffected != 1 {
			return out, ErrConflict
		}
		row.IterationID, row.VersionID, row.MilestoneID, row.Version = in.IterationID, in.VersionID, in.MilestoneID, version+1
	}
	out = assignmentView(row)
	if automation != nil {
		return out, m.recordAutomationPlanningOn(ctx, tx, row.ID, out.Version, *automation)
	}
	return out, m.workChangedOn(ctx, tx, tenant, project, row.ID, out.Version, "planning.assignment_changed", false, work.String())
}
func (m *Module) changePlanningStateOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind string, version int, state, reason string, target *guid.ID) (PlanningEntity, error) {
	var out PlanningEntity
	if err := m.planningStateAuthorityOn(ctx, tx, tenant, project, id, kind, state); err != nil {
		return out, err
	}
	row, err := m.planningEntityOn(ctx, tx, tenant, project, id, kind)
	if err != nil {
		return out, err
	}
	if row.Version != version {
		return out, ErrConflict
	}
	values := map[string]any{"version": version + 1, "updated_at": m.now()}
	switch kind {
	case "iterations":
		if !slices.Contains([]string{"ACTIVE", "CLOSED"}, state) || row.State == "CLOSED" || row.State == state {
			return out, ErrInvalid
		}
		values["state"] = state
		if state == "CLOSED" {
			if err = m.carryIterationOn(ctx, tx, tenant, project, row, target); err != nil {
				return out, err
			}
			values["closed_at"] = m.now()
		}
	case "versions":
		if state == "LOCKED" {
			if row.State != "PLANNING" {
				return out, ErrConflict
			}
			values["state"], values["locked_at"], values["locked_by"] = "LOCKED", m.now(), authn.PrincipalIDFromContext(ctx)
		} else if state == "PLANNING" {
			if row.State != "LOCKED" || strings.TrimSpace(reason) == "" {
				return out, ErrInvalid
			}
			values["state"], values["locked_at"], values["locked_by"] = "PLANNING", nil, nil
		} else if state == "RELEASED" && slices.Contains([]string{"PLANNING", "LOCKED"}, row.State) {
			values["state"], values["released_at"] = state, m.now()
		} else if state == "ARCHIVED" && row.State == "RELEASED" {
			values["state"] = state
		} else {
			return out, ErrInvalid
		}
	case "milestones":
		if state != "ACHIEVED" || row.AchievedAt != nil {
			return out, ErrInvalid
		}
		if err = m.milestoneReadyOn(ctx, tx, tenant, project, id); err != nil {
			return out, err
		}
		values["achieved_at"] = m.now()
	default:
		return out, ErrInvalid
	}
	result := m.planningStore(tx.WithContext(ctx), kind).Where(map[string]any{"tenant_id": tenant, "project_id": project, "id": id, "version": version}).Updates(values)
	if result.Error != nil {
		return out, result.Error
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	out, err = m.planningEntityOn(ctx, tx, tenant, project, id, kind)
	if err != nil {
		return out, err
	}
	return out, m.workChangedOn(ctx, tx, tenant, project, id, out.Version, "planning.entity_changed", false, reason)
}
func (m *Module) milestoneReadyOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if m.planningWork == nil {
		return ErrUnavailable
	}
	q := query.BindWorkPlanning(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.MilestoneID.Eq(id)).Order(q.WorkItemID).Limit(1001).Find()
	if err != nil {
		return err
	}
	if len(rows) == 0 || len(rows) > 1000 {
		return ErrInvalid
	}
	for _, row := range rows {
		fact, err := m.planningWork(ctx, tx, tenant, project, row.WorkItemID)
		if err != nil {
			return err
		}
		if fact.State != "ACCEPTED" {
			return ErrConflict
		}
	}
	return nil
}
func (m *Module) carryIterationOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, iteration PlanningEntity, target *guid.ID) error {
	if m.planningWork == nil {
		return ErrUnavailable
	}
	if target != nil {
		if *target == iteration.ID {
			return ErrInvalid
		}
		next, err := m.planningEntityOn(ctx, tx, tenant, project, *target, "iterations")
		if err != nil {
			return err
		}
		if !slices.Contains([]string{"PLANNED", "ACTIVE"}, next.State) {
			return ErrIterationClosed
		}
	}
	q := query.BindWorkPlanning(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.IterationID.Eq(iteration.ID)).Order(q.WorkItemID).Limit(1001).Find()
	if err != nil {
		return err
	}
	if len(rows) > 1000 {
		return ErrUnavailable
	}
	for _, row := range rows {
		fact, err := m.planningWork(ctx, tx, tenant, project, row.WorkItemID)
		if err != nil {
			return err
		}
		workflow, err := m.effectiveWorkflowOn(ctx, tx, tenant, project, fact.Type)
		if err != nil {
			return err
		}
		group := ""
		for _, state := range workflow.States {
			if state.MapsTo == fact.State {
				group = state.Group
			}
		}
		if fact.State == "ACCEPTED" || group == "DONE" || group == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
			continue
		}
		var destination *guid.ID
		if target != nil {
			destination = target
		} else if workflow.CarryOverMode != "BACKLOG" {
			i := query.BindIterations(tx, m.names)
			candidates, err := i.WithContext(ctx).Where(i.TenantID.Eq(tenant), i.ProjectID.Eq(project), i.ID.Neq(iteration.ID), i.State.In("PLANNED", "ACTIVE"), i.StartsAt.Gte(*iteration.EndsAt)).Order(i.StartsAt, i.ID).Limit(1).Find()
			if err != nil {
				return err
			}
			if len(candidates) > 0 {
				destination = &candidates[0].ID
			}
		}
		if _, err := m.assignPlanningOn(ctx, tx, tenant, project, row.WorkItemID, row.Version, PlanningAssignmentInput{IterationID: destination, VersionID: row.VersionID, MilestoneID: row.MilestoneID}); err != nil {
			return err
		}
		value, err := m.ids.Next()
		if err != nil {
			return err
		}
		if value <= 0 {
			return ErrUnavailable
		}
		receipt := &model.PlanningCarryover{ID: guid.ID(value), TenantID: tenant, ProjectID: project, WorkItemID: row.WorkItemID, PrincipalID: fact.PrincipalID, FromIterationID: iteration.ID, ToIterationID: destination, CarriedAt: m.now()}
		if err := query.BindPlanningCarryovers(tx, m.names).WithContext(ctx).Create(receipt); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) planningStateAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind, state string) error {
	err := m.projectAuthorityOn(ctx, tx, tenant, project, "workflow_manage")
	if err == nil {
		return nil
	}
	if kind != "versions" || state != "PLANNING" {
		return err
	}
	if readErr := m.projectAuthorityOn(ctx, tx, tenant, project, "work_item_read"); readErr != nil {
		return readErr
	}
	row, readErr := m.planningEntityOn(ctx, tx, tenant, project, id, kind)
	if readErr != nil {
		return readErr
	}
	if row.CreatedBy != authn.PrincipalIDFromContext(ctx) {
		return ErrForbidden
	}
	return nil
}
