package delivery

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
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
	ErrDependencyCycle   = errors.New("delivery: dependency would create a cycle")
	ErrDependencyLimit   = errors.New("delivery: maximum 50 direct predecessors")
	ErrDependencyProject = errors.New("delivery: predecessor belongs to another project")
)

type dependencyView struct {
	ID        guid.ID `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	State     string  `json:"state"`
	Satisfied bool    `json:"satisfied"`
}

type dependencyCycleError struct{ Cycle []string }

func (e *dependencyCycleError) Error() string { return ErrDependencyCycle.Error() }
func (e *dependencyCycleError) Unwrap() error { return ErrDependencyCycle }

// The project lock serializes graph edits. Load current edges once, including
// after a transaction established an earlier IAM snapshot, then search in memory.
func (m *Module) dependencyCycleOn(ctx context.Context, tx *gorm.DB, tenant, project, work, predecessor guid.ID) ([]guid.ID, error) {
	q := query.BindDependencies(tx, m.service.names)
	rows, err := q.WithContext(ctx).Select(q.WorkItemID, q.DependsOnID).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Order(q.WorkItemID, q.DependsOnID).Find()
	if err != nil {
		return nil, err
	}
	return dependencyCyclePath(rows, work, predecessor), nil
}

func dependencyCyclePath(edges []*model.WorkItemDependency, work, predecessor guid.ID) []guid.ID {
	graph := make(map[guid.ID][]guid.ID)
	for _, edge := range edges {
		graph[edge.WorkItemID] = append(graph[edge.WorkItemID], edge.DependsOnID)
	}
	pending := []guid.ID{predecessor}
	parents := map[guid.ID]guid.ID{predecessor: 0}
	for head := 0; head < len(pending); head++ {
		id := pending[head]
		if id == work {
			path := []guid.ID{}
			for at := work; at != 0; at = parents[at] {
				path = append(path, at)
			}
			slices.Reverse(path)
			return append([]guid.ID{work}, path...)
		}
		for _, next := range graph[id] {
			if _, seen := parents[next]; !seen {
				parents[next] = id
				pending = append(pending, next)
			}
		}
	}
	return nil
}

func (m *Module) changeDependencyOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner, predecessor guid.ID, version int, remove bool) (WorkView, error) {
	var empty WorkView
	if predecessor <= 0 || predecessor == id {
		return empty, ErrInvalidArgument
	}
	_, work, active, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_write")
	if err != nil {
		return empty, err
	}
	if version > 0 && work.Version != version {
		return empty, ErrWorkVersion
	}
	if !slices.Contains([]string{"DRAFT", "READY", "BLOCKED", "CLAIMED", "IN_PROGRESS", "REWORK"}, work.State) || work.IsContainer {
		return empty, ErrWorkPreparationState
	}
	// Removing an existing edge must remain possible if the predecessor was
	// archived. Creation requires an actual readable work item in this project.
	if !remove {
		w := query.BindWorkItems(tx, m.service.names)
		policy, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
		if e != nil {
			return empty, e
		}
		scope, e := workScope(policy, project, w.CreatedBy)
		if e != nil {
			return empty, e
		}
		base := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(predecessor), w.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		row, e := base.First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			// Distinguish a readable foreign target without turning arbitrary
			// IDs into a cross-project existence oracle. Never lock foreign
			// work under this project's lock: no foreign mutation is allowed.
			foreign, lookupErr := w.WithContext(ctx).Select(w.ProjectID, w.CreatedBy).Where(w.TenantID.Eq(tenant), w.ID.Eq(predecessor), w.ProjectID.Neq(project), w.ArchivedAt.IsNull()).First()
			if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
				return empty, lookupErr
			}
			if lookupErr == nil {
				policy, policyErr := m.workPolicy(ctx, tx, tenant, foreign.ProjectID, "work_item_read", actor)
				if policyErr != nil {
					return empty, policyErr
				}
				scope, scopeErr := workScope(policy, foreign.ProjectID, w.CreatedBy)
				if scopeErr != nil {
					return empty, scopeErr
				}
				visible := w.WithContext(ctx).Select(w.ID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(foreign.ProjectID), w.ID.Eq(predecessor), w.ArchivedAt.IsNull())
				if scope != nil {
					visible = visible.Where(scope)
				}
				rows, visibleErr := visible.Limit(1).Find()
				if visibleErr != nil {
					return empty, visibleErr
				}
				if len(rows) > 0 {
					return empty, ErrDependencyProject
				}
			}
			return empty, ErrProjectNotFound
		} else if e != nil {
			return empty, e
		}
		if row.State == "CANCELLED" { //nolint:misspell // Frozen business state.
			return empty, ErrWorkPreparationState
		}
	}
	q := query.BindDependencies(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(id))
	edges, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).Find()
	if err != nil {
		return empty, err
	}
	exists := false
	for _, e := range edges {
		exists = exists || e.DependsOnID == predecessor
	}
	if remove && !exists || !remove && exists {
		return workRecord(work), nil
	}
	version = work.Version
	if !remove {
		if len(edges) >= 50 {
			return empty, ErrDependencyLimit
		}
		cycle, e := m.dependencyCycleOn(ctx, tx, tenant, project, id, predecessor)
		if e != nil {
			return empty, e
		}
		if len(cycle) > 0 {
			w := query.BindWorkItems(tx, m.service.names)
			policy, e := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
			if e != nil {
				return empty, e
			}
			scope, e := workScope(policy, project, w.CreatedBy)
			if e != nil {
				return empty, e
			}
			ids := make([]driver.Valuer, len(cycle))
			for i, id := range cycle {
				ids[i] = id
			}
			base := w.WithContext(ctx).Select(w.ID, w.Number).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.In(ids...), w.ArchivedAt.IsNull())
			if scope != nil {
				base = base.Where(scope)
			}
			rows, e := base.Find()
			if e != nil {
				return empty, e
			}
			numbers := make(map[guid.ID]int, len(rows))
			for _, row := range rows {
				numbers[row.ID] = row.Number
			}
			labels := make([]string, len(cycle))
			for i, id := range cycle {
				number, visible := numbers[id]
				if !visible {
					// Detect the complete graph, but never disclose a hidden work
					// item through an otherwise authorized command's error.
					return empty, ErrDependencyCycle
				}
				labels[i] = fmt.Sprintf("WI-%d", number)
			}
			return empty, &dependencyCycleError{Cycle: labels}
		}
		err = q.WithContext(ctx).Create(&model.WorkItemDependency{TenantID: tenant, ProjectID: project, WorkItemID: id, DependsOnID: predecessor})
	} else {
		_, err = base.Where(q.DependsOnID.Eq(predecessor)).Delete()
	}
	if err != nil {
		return empty, err
	}
	issues, blocks, err := m.evaluatePreparationOn(ctx, tx, work, active)
	if err != nil {
		return empty, err
	}
	before := work.State
	work.ReadinessOK = len(issues) == 0
	encoded, _ := json.Marshal(issues)
	work.ReadinessIssues = string(encoded)
	encoded, _ = json.Marshal(blocks)
	work.BlockReasons = string(encoded)
	if work.PausedAt == nil && slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, work.State) {
		switch {
		case len(blocks) > 0:
			work.State = "BLOCKED"
		case !work.ReadinessOK:
			work.State = "DRAFT"
		default:
			work.State = "READY"
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	work.Version++
	work.UpdatedAt = now
	work.UpdatedBy = &actor
	if before != work.State {
		work.StateChangedAt = now
	}
	w := query.BindWorkItems(tx, m.service.names)
	result, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.Version.Eq(version)).Select(w.State, w.ReadinessOK, w.ReadinessIssues, w.BlockReasons, w.StateChangedAt, w.Version, w.UpdatedAt, w.UpdatedBy).Updates(work)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	reason := "DEPENDENCY_ADDED"
	if remove {
		reason = "DEPENDENCY_REMOVED"
	}
	transition := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(value), ProjectID: project, WorkItemID: id, FromState: &before, ToState: work.State, Trigger: "COMMAND", ActorKind: "HUMAN", ActorID: &actor, AuthoritySource: "permission:work_item_write", ReasonCode: reason, VersionAfter: work.Version, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.work_dependency_changed", "work_item", id)
	event.Detail["depends_on_id"] = predecessor.String()
	event.Detail["removed"] = remove
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.dependency_changed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: transition.AuthoritySource}, Subject: events.Subject{Kind: "work_item", ID: id}, SubjectVersion: int64(work.Version), Data: map[string]any{"depends_on_id": predecessor.String(), "removed": remove, "state": work.State}})
	if err != nil {
		return empty, err
	}
	return workRecord(work), nil
}

func (m *Module) dependenciesOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, downstream ...bool) ([]dependencyView, error) {
	if _, err := m.readWork(ctx, tx, tenant, project, id, actor); err != nil {
		return nil, err
	}
	q := query.BindDependencies(tx, m.service.names)
	baseEdges := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project))
	reverse := len(downstream) > 0 && downstream[0]
	if reverse {
		baseEdges = baseEdges.Where(q.DependsOnID.Eq(id))
	} else {
		baseEdges = baseEdges.Where(q.WorkItemID.Eq(id))
	}
	edges, err := baseEdges.Order(q.DependsOnID).Find()
	if err != nil {
		return nil, err
	}
	items := []dependencyView{}
	if len(edges) == 0 {
		return items, nil
	}
	w := query.BindWorkItems(tx, m.service.names)
	constraint, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
	if err != nil {
		return nil, err
	}
	scope, err := workScope(constraint, project, w.CreatedBy)
	if err != nil {
		return nil, err
	}
	ids := make([]driver.Valuer, 0, len(edges))
	for _, e := range edges {
		if reverse {
			ids = append(ids, e.WorkItemID)
		} else {
			ids = append(ids, e.DependsOnID)
		}
	}
	base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.In(ids...))
	if scope != nil {
		base = base.Where(scope)
	}
	rows, err := base.Order(w.Number).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		items = append(items, dependencyView{ID: row.ID, Number: row.Number, Title: row.Title, State: row.State, Satisfied: row.State == "ACCEPTED"})
	}
	return items, nil
}
