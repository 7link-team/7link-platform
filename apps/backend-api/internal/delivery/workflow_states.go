package delivery

import (
	"context"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WorkflowDisplayStates owns display provenance without exposing storage rows.
// All calls share the configuration command's transaction and current IAM fence.
type WorkflowDisplayStates struct {
	BindResolver func(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string) (string, error)) error
	ProjectsOn   func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error)
	ItemsOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]WorkflowDisplayItem, error)
	MigrateOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, WorkflowDisplayItem, string) error
}

type WorkflowDisplayItem struct {
	ID         guid.ID
	State, Key string
	Version    int
}

func (m *Module) provideWorkflowStates(s *module.Services) error {
	return module.Provide[WorkflowDisplayStates](s, WorkflowDisplayStates{BindResolver: func(resolve func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string) (string, error)) error {
		if resolve == nil || m.workflowDisplayKey != nil {
			return ErrInvalidArgument
		}
		m.workflowDisplayKey = resolve
		return nil
	}, ProjectsOn: m.workflowProjectsOn, ItemsOn: m.workflowItemsOn, MigrateOn: m.migrateWorkflowStateOn})
}

func (m *Module) workflowProjectsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) ([]guid.ID, error) {
	if !isWorkActivityTransaction(tx) || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrWorkForbidden
	}
	q := query.BindProjects(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ArchivedAt.IsNull()).Order(q.ProjectID).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		if err := m.workflowStateAuthorityOn(ctx, tx, tenant, row.ProjectID); err != nil {
			return nil, err
		}
		ids = append(ids, row.ProjectID)
	}
	return ids, nil
}

func (m *Module) workflowItemsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind string) ([]WorkflowDisplayItem, error) {
	if !isWorkActivityTransaction(tx) {
		return nil, ErrInvalidArgument
	}
	if err := m.workflowStateAuthorityOn(ctx, tx, tenant, project); err != nil {
		return nil, err
	}
	q := query.BindWorkItems(tx, m.service.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Type.Eq(kind), q.ArchivedAt.IsNull()).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowDisplayItem, 0, len(rows))
	for _, row := range rows {
		key, err := m.workDisplayKeyOn(ctx, tx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, WorkflowDisplayItem{ID: row.ID, State: row.State, Key: key, Version: row.Version})
	}
	return out, nil
}

func (m *Module) migrateWorkflowStateOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, item WorkflowDisplayItem, target string) error {
	if !isWorkActivityTransaction(tx) || target == "" || target == item.Key {
		return ErrInvalidArgument
	}
	if err := m.workflowStateAuthorityOn(ctx, tx, tenant, project); err != nil {
		return err
	}
	q := query.BindWorkItems(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(item.ID), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return err
	}
	key, err := m.workDisplayKeyOn(ctx, tx, row)
	if err != nil {
		return err
	}
	if row.Version != item.Version || row.State != item.State || key != item.Key {
		return ErrWorkVersion
	}
	actor, now := authn.PrincipalIDFromContext(ctx), time.Now().UTC().Truncate(time.Millisecond)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(item.Version)).Updates(map[string]any{"version": item.Version + 1, "updated_at": now, "updated_by": actor})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	source := "workflow-state:" + guid.ID(id).String()
	receipt := &model.WorkItemTransition{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: row.ID, FromState: &row.State, ToState: row.State, FromStateKey: &item.Key, ToStateKey: &target, Trigger: "COMMAND", ActorKind: "HUMAN", ActorID: &actor, AuthoritySource: "permission:workflow_manage", ReasonCode: "DISPLAY_STATE_MIGRATED", SourceRef: &source, VersionAfter: item.Version + 1, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(receipt); err != nil {
		return err
	}
	if _, err = m.recordWorkActivityOn(ctx, tx, tenant, row.ID, WorkActivityInput{Kind: "TRANSITIONED", ActorKind: "HUMAN", ActorID: actor, RefKind: "transition", RefID: receipt.ID, Title: "Work item display state migrated"}); err != nil {
		return err
	}
	a := audit.NewDomainEvent(ctx, tenant, "delivery.work_item_transitioned", "work_item", row.ID)
	a.Detail["transition_id"], a.Detail["from_key"], a.Detail["to_key"] = receipt.ID.String(), item.Key, target
	if err = m.service.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.transitioned", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:workflow_manage"}, Subject: events.Subject{Kind: "work_item", ID: row.ID}, SubjectVersion: int64(item.Version + 1), Data: map[string]any{"transition_id": receipt.ID.String(), "from_state_key": item.Key, "to_state_key": target}})
	return err
}

func (m *Module) resolveWorkflowDisplayKeyOn(ctx context.Context, tx *gorm.DB, row *model.WorkItem, key string) (string, error) {
	if m.workflowDisplayKey == nil {
		return key, nil
	}
	return m.workflowDisplayKey(ctx, tx, row.TenantID, row.ProjectID, row.Type, row.State, key)
}

func (m *Module) workflowStateAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || !isWorkActivityTransaction(tx) {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrWorkForbidden
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || human.ID != actor {
		return ErrWorkForbidden
	}
	return m.authorizeOn(ctx, tx, tenant, project, "workflow_manage")
}
