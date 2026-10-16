package fleet

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func directoryHarness(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "fleet.sqlite"), Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("dir_")), Config{Config: module.Config{Enabled: true}})
	source := m.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.names.Agents.String()).Create(map[string]any{"id": 1, "tenant_id": 7, "principal_id": 101, "name": "Agent", "handle": "agent-one", "avatar_seed": 1, "system_prompt": "Private prompt", "runtime_kind": "CODEX", "model_id": "model", "model_family": "family", "created_by_principal_id": 11, "owner_principal_id": 11, "created_at": now, "updated_at": now}).Error)
	for role, key := range map[string]string{"EXECUTOR": "agent_executor", "REVIEWER": "agent_reviewer"} {
		require.NoError(t, db.Table(m.names.AgentRoles.String()).Create(map[string]any{"tenant_id": 7, "agent_id": 1, "role": role, "iam_role_key": key, "created_at": now, "updated_at": now}).Error)
	}
	return m, db
}

func TestDirectoryIsolationRevocationAndRollback(t *testing.T) {
	m, db := directoryHarness(t)
	abort := errors.New("rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		view, found, err := m.ByPrincipalOn(t.Context(), tx, 7, 101)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, guid.ID(1), view.ID)
		require.Equal(t, guid.ID(11), view.OwnerPrincipalID)
		require.Equal(t, []string{"EXECUTOR", "REVIEWER"}, view.Roles)
		require.Equal(t, []string{"agent_executor", "agent_reviewer"}, view.IAMRoleKeys)
		require.Equal(t, "DRAFT", view.State)
		require.Equal(t, "HUMAN_CREATED", view.Origin)
		for _, ids := range [][2]guid.ID{{8, 101}, {7, 999}} {
			v, ok, e := m.ByPrincipalOn(t.Context(), tx, ids[0], ids[1])
			require.NoError(t, e)
			require.False(t, ok)
			require.Zero(t, v)
		}
		view.IAMRoleKeys[0] = "team_owner"
		roles := query.BindAgentRoles(tx, m.names)
		_, err = roles.WithContext(t.Context()).Where(roles.AgentID.Eq(guid.ID(1)), roles.Role.Eq("EXECUTOR")).Update(roles.RevokedAt, time.Now().UTC())
		require.NoError(t, err)
		view, found, err = m.ByPrincipalOn(t.Context(), tx, 7, 101)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, []string{"agent_reviewer"}, view.IAMRoleKeys)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, ok, err := m.ByPrincipalOn(t.Context(), tx, 7, 101)
		require.NoError(t, err)
		require.True(t, ok)
		require.Len(t, v.Roles, 2)
		return nil
	}))
}

func TestDirectoryErrorsAndIncompatibleRoles(t *testing.T) {
	m, db := directoryHarness(t)
	_, _, err := m.ByPrincipalOn(t.Context(), db, 7, 101)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = (*Module)(nil).ByPrincipalOn(t.Context(), nil, 7, 101)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, ids := range [][2]guid.ID{{0, 101}, {7, 0}} {
			_, _, e := m.ByPrincipalOn(t.Context(), tx, ids[0], ids[1])
			require.ErrorIs(t, e, ErrInvalidArgument)
		}
		_, _, e := m.ByPrincipalOn(t.Context(), nil, 7, 101)
		require.ErrorIs(t, e, ErrInvalidArgument)
		for _, role := range []string{"ACCEPTOR", "ASSISTANT"} {
			key, ok := AgentIAMRoleKey(role)
			require.True(t, ok)
			require.NoError(t, tx.Table(m.names.AgentRoles.String()).Create(map[string]any{"tenant_id": 7, "agent_id": 1, "role": role, "iam_role_key": key, "created_at": time.Now(), "updated_at": time.Now()}).Error)
			v, found, e := m.ByPrincipalOn(t.Context(), tx, 7, 101)
			require.ErrorIs(t, e, ErrInvalidRoles)
			require.False(t, found)
			require.Zero(t, v)
			require.NoError(t, tx.Table(m.names.AgentRoles.String()).Where("role = ?", role).Delete(nil).Error)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, e = m.ByPrincipalOn(ctx, tx, 7, 101)
		require.ErrorIs(t, e, context.Canceled)
		require.NoError(t, tx.Migrator().DropTable(m.names.AgentRoles.String()))
		_, _, e = m.ByPrincipalOn(t.Context(), tx, 7, 101)
		require.Error(t, e)
		return nil
	}))
	for _, role := range []string{"EXECUTOR", "REVIEWER", "COORDINATOR", "LEAD", "TESTER", "ACCEPTOR", "ASSISTANT"} {
		key, ok := AgentIAMRoleKey(role)
		require.True(t, ok)
		require.NotEmpty(t, key)
	}
	key, ok := AgentIAMRoleKey("agent_executor")
	require.False(t, ok)
	require.Empty(t, key)
}

func TestDirectoryProvider(t *testing.T) {
	m, _ := directoryHarness(t)
	require.Equal(t, "fleet", m.ModuleName())
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	directory, err := module.Resolve[AgentDirectory](services)
	require.NoError(t, err)
	require.NotNil(t, directory.ByPrincipalOn)
}
