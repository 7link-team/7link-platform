package yunxiao

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowArchiveImpactMigrationAndProjectOverrides(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	items := []delivery.WorkflowDisplayItem{{ID: 501, State: "READY", Key: "ready", Version: 3}, {ID: 502, State: "READY", Key: "ready", Version: 4}, {ID: 503, State: "READY", Key: "ready", Version: 5}}
	m.workflowStates = delivery.WorkflowDisplayStates{
		ProjectsOn: func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{98, 99}, nil },
		ItemsOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ string) ([]delivery.WorkflowDisplayItem, error) {
			return items, nil
		},
		MigrateOn: func(_ context.Context, tx *gorm.DB, _, project guid.ID, item delivery.WorkflowDisplayItem, target string) error {
			require.EqualValues(t, 98, project)
			require.Equal(t, "scheduled", target)
			return tx.Exec("INSERT INTO test_effects (kind) VALUES (?)", item.ID.String()).Error
		},
	}
	body := WorkflowInput{States: []StateDefinition{{Key: "ready", DisplayName: "Ready", Group: "TODO", MapsTo: "READY"}, {Key: "scheduled", DisplayName: "Scheduled", Group: "TODO", MapsTo: "READY"}}, Transitions: []TransitionDefinition{}}
	save := func(project guid.ID, version int, body WorkflowInput) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.saveWorkflowOn(ctx, tx, 1, project, "TASK", version, body, false)
			return err
		})
	}
	require.NoError(t, save(0, 0, body))
	// Explicit active project override must survive a team archive.
	body.States[0].DisplayName = "Project ready"
	require.NoError(t, save(99, 0, body))
	body.States[0].DisplayName = "Ready"
	body.States[0].Archived = true
	var preview WorkflowPreview
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		preview, err = m.previewWorkflowOn(ctx, tx, 1, 0, "TASK", 1, body, false)
		return err
	}))
	require.Equal(t, "EVALUATED", preview.RuntimeImpact)
	require.Equal(t, 1, preview.ProjectsAffected)
	require.Equal(t, 3, preview.ItemsInArchivedStates)
	var before int64
	require.NoError(t, db.Table("test_effects").Count(&before).Error)
	require.Error(t, save(0, 1, body), "occupied archive requires a target")
	var after int64
	require.NoError(t, db.Table("test_effects").Count(&after).Error)
	require.Equal(t, before, after)
	body.States[0].MigrateTo = "scheduled"
	body.States[1].MapsTo = "DRAFT"
	require.ErrorIs(t, save(0, 1, body), ErrInvalid)
	body.States[1].MapsTo = "READY"
	original := m.workflowStates.MigrateOn
	m.workflowStates.MigrateOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, delivery.WorkflowDisplayItem, string) error {
		return errors.New("source failed")
	}
	require.Error(t, save(0, 1, body))
	m.workflowStates.MigrateOn = original
	require.NoError(t, save(0, 1, body))
	require.NoError(t, db.Table("test_effects").Count(&after).Error)
	require.Equal(t, before+5, after, "three migrations plus two configuration audit/outbox effects")
	request := delivery.WorkflowTransition{TenantID: 1, ProjectID: 98, Type: "TASK", From: "DRAFT", FromKey: "draft", ToKey: "ready", ActorKind: "HUMAN"}
	_, err := m.selectWorkflowStateOn(ctx, db, request)
	require.Error(t, err, "an archived state cannot be a new target")
}

func TestWorkflowDisplayResolverPreservesActiveKeysAndOnlyValidArchiveMigration(t *testing.T) {
	m, db, ctx := workHarness(t)
	key, e := m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "READY", "ready")
	require.NoError(t, e)
	require.Equal(t, "ready", key)
	_, e = m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "DRAFT", "ready")
	require.ErrorIs(t, e, ErrConflict)
	_, e = m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "READY", "missing")
	require.ErrorIs(t, e, ErrConflict)
	body := WorkflowInput{States: []StateDefinition{{Key: "ready", DisplayName: "Ready", Group: "TODO", MapsTo: "READY", Archived: true, MigrateTo: "scheduled"}, {Key: "scheduled", DisplayName: "Scheduled", Group: "TODO", MapsTo: "READY"}}, Transitions: []TransitionDefinition{}}
	raw, e := json.Marshal(body)
	require.NoError(t, e)
	require.NoError(t, db.Table(m.names.WorkflowConfigs.String()).Create(map[string]any{"id": 900, "tenant_id": 1, "project_id": 99, "work_item_type": "TASK", "document": string(raw), "version": 1, "updated_at": m.now(), "updated_by": 11}).Error)
	key, e = m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "READY", "ready")
	require.NoError(t, e)
	require.Equal(t, "scheduled", key)
	body.States[0].MigrateTo = "missing"
	raw, e = json.Marshal(body)
	require.NoError(t, e)
	require.NoError(t, db.Table(m.names.WorkflowConfigs.String()).Where("id=900").Update("document", string(raw)).Error)
	_, e = m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "READY", "ready")
	require.ErrorIs(t, e, ErrConflict)
	require.NoError(t, db.Table(m.names.WorkflowConfigs.String()).Where("id=900").Update("document", "[").Error)
	_, e = m.workflowDisplayKeyOn(ctx, db, 1, 99, "TASK", "READY", "ready")
	require.Error(t, e)
}
