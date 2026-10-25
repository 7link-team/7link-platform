package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkflowDisplayMigrationPreservesBusinessFactsAndRejectsStaleSource(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	policy := m.workPolicy
	m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "workflow_manage" && tenant == 1 && project == 99 && actor == 11 {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	var item WorkflowDisplayItem
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		items, err := m.workflowItemsOn(ctx, tx, 1, 99, "TASK")
		require.NoError(t, err)
		for _, found := range items {
			if found.ID == id {
				item = found
			}
		}
		require.Equal(t, "ready", item.Key)
		return m.migrateWorkflowStateOn(ctx, tx, 1, 99, item, "scheduled")
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
		require.NoError(t, err)
		require.Equal(t, item.State, row.State)
		require.Equal(t, item.Version+1, row.Version)
		key, err := m.workDisplayKeyOn(ctx, tx, row)
		require.NoError(t, err)
		require.Equal(t, "scheduled", key)
		return nil
	}))
	before := transitionSnapshot(t, m, db, id)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.migrateWorkflowStateOn(ctx, tx, 1, 99, item, "other") }), ErrWorkVersion)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	// A later command failure rolls back the version, immutable receipt, activity,
	// audit and outbox together, including an earlier valid display migration.
	abort := errors.New("abort configuration")
	err := db.Transaction(func(tx *gorm.DB) error {
		item.Key, item.Version = "scheduled", item.Version+1
		if err := m.migrateWorkflowStateOn(ctx, tx, 1, 99, item, "ready"); err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.workflowItemsOn(ctx, tx, 2, 99, "TASK"); return err }))
}

func TestWorkflowDisplayCapabilityRejectsAgentAndRevokedAuthority(t *testing.T) {
	m, db, ctx, id := transitionFixture(t)
	original := m.workPolicy
	m.workPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "workflow_manage" && tenant == 1 && project == 99 {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		return original(ctx, tx, tenant, project, permission, actor)
	}
	services := module.NewServices()
	require.NoError(t, m.provideWorkflowStates(services))
	require.NoError(t, services.Seal())
	capability, err := module.Resolve[WorkflowDisplayStates](services)
	require.NoError(t, err)
	require.Error(t, capability.BindResolver(nil))
	require.NoError(t, capability.BindResolver(func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _, _, key string) (string, error) { return key, nil }))
	require.Error(t, capability.BindResolver(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, string) (string, error) {
		return "", nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		projects, err := capability.ProjectsOn(ctx, tx, 1)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{99}, projects)
		return nil
	}))
	before := transitionSnapshot(t, m, db, id)
	agent := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 14, SubjectType: authn.SubjectTypePrincipal})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := capability.ItemsOn(agent, tx, 1, 99, "TASK"); return err }))
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := capability.ItemsOn(ctx, tx, 1, 99, "TASK"); return err }))
	require.Equal(t, before, transitionSnapshot(t, m, db, id))
}
