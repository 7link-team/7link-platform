package yunxiao

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	yi18n "7link.com/platform/internal/yunxiao/i18n"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A team change is evaluated through each project's existing override. Display
// migration does not execute or imply a canonical business-state transition.
func (m *Module) workflowImpactOn(ctx context.Context, tx *gorm.DB, tenant, scope guid.ID, kind string, projects []guid.ID, preview *WorkflowPreview, apply bool) error {
	if m.workflowStates.ItemsOn == nil || m.workflowStates.MigrateOn == nil {
		for _, state := range preview.After.States {
			if state.Archived {
				return ErrUnavailable
			}
		}
		return nil
	}
	preview.RuntimeImpact = "EVALUATED"
	preview.ProjectsAffected, preview.ItemsInArchivedStates = 0, 0
	type migration struct {
		project guid.ID
		item    delivery.WorkflowDisplayItem
		target  string
	}
	var pending []migration
	for _, project := range projects {
		before, err := m.effectiveWorkflowOn(ctx, tx, tenant, project, kind)
		if err != nil {
			return err
		}
		after := preview.After
		after.States = slices.Clone(after.States)
		after.Transitions = slices.Clone(after.Transitions)
		if scope == 0 {
			q := query.BindWorkflowConfigs(tx, m.names)
			row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemType.Eq(kind)).First()
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && row.Document != "" {
				overlay, err := workflowView(row)
				if err != nil {
					return err
				}
				after = mergeWorkflow(after, overlay)
			}
		}
		if reflect.DeepEqual(before.WorkflowInput, after.WorkflowInput) {
			continue
		}
		preview.ProjectsAffected++
		states, old := map[string]StateDefinition{}, map[string]StateDefinition{}
		for _, state := range before.States {
			old[state.Key] = state
		}
		for _, state := range after.States {
			states[state.Key] = state
		}
		for _, state := range states {
			if state.MigrateTo == "" {
				continue
			}
			target, ok := states[state.MigrateTo]
			if !state.Archived || !ok || target.Archived || target.Key == state.Key || target.Group != state.Group || target.MapsTo != state.MapsTo {
				return ErrInvalid
			}
		}
		items, err := m.workflowStates.ItemsOn(ctx, tx, tenant, project, kind)
		if err != nil {
			return err
		}
		for _, item := range items {
			state, exists := states[item.Key]
			previous, wasDefined := old[item.Key]
			if exists && !state.Archived {
				// Editing a display key cannot silently reinterpret occupied work.
				if state.MapsTo != item.State || (wasDefined && state.Group != previous.Group) {
					return ErrInvalid
				}
				continue
			}
			preview.ItemsInArchivedStates++
			target, ok := states[state.MigrateTo]
			if !exists || !ok || target.Archived || target.Group != state.Group || target.MapsTo != item.State || state.MapsTo != item.State {
				if apply {
					return respx.ErrStatus(ctx, 409, 126003, yi18n.StateInUse, map[string]any{"state_key": item.Key, "work_item_id": item.ID})
				}
				continue
			}
			pending = append(pending, migration{project: project, item: item, target: target.Key})
		}
	}
	if apply {
		for _, move := range pending {
			if err := m.workflowStates.MigrateOn(ctx, tx, tenant, move.project, move.item, move.target); err != nil {
				return err
			}
		}
	}
	return nil
}

// Resolve canonical receipts (including newly created work) through current
// display metadata. An archived default can only resolve to its valid alias.
func (m *Module) workflowDisplayKeyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, kind, state, key string) (string, error) {
	doc, err := m.effectiveWorkflowOn(ctx, tx, tenant, project, kind)
	if err != nil {
		return "", err
	}
	states := map[string]StateDefinition{}
	for _, s := range doc.States {
		states[s.Key] = s
	}
	source, ok := states[key]
	if !ok || source.MapsTo != state {
		return "", ErrConflict
	}
	if !source.Archived {
		return key, nil
	}
	target, ok := states[source.MigrateTo]
	if !ok || target.Archived || target.Group != source.Group || target.MapsTo != state {
		return "", ErrConflict
	}
	return target.Key, nil
}
