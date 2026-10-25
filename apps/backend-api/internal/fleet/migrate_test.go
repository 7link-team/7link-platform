package fleet

import (
	goosex "7link.com/platform/pkg/extension/goose"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestWorkspaceWriteMigrationPreservesPopulatedAgentsRolesAndIndexes(t *testing.T) {
	m, db := directoryHarness(t)
	raw, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, "sqlite", 11))
	read := func(table string) string {
		var rows []map[string]any
		require.NoError(t, db.Table(table).Order("1").Find(&rows).Error)
		b, e := json.Marshal(rows)
		require.NoError(t, e)
		return string(b)
	}
	agents, roles := read(m.names.Agents.String()), read(m.names.AgentRoles.String())
	var beforeIndexes []string
	require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? ORDER BY name", m.names.Agents.String()).Scan(&beforeIndexes).Error)
	require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, "sqlite", 12))
	require.JSONEq(t, agents, read(m.names.Agents.String()))
	require.JSONEq(t, roles, read(m.names.AgentRoles.String()))
	var afterIndexes []string
	require.NoError(t, db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? ORDER BY name", m.names.Agents.String()).Scan(&afterIndexes).Error)
	require.Equal(t, beforeIndexes, afterIndexes)
	var violations []map[string]any
	require.NoError(t, db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error)
	require.Empty(t, violations)
	require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 1).Update("authorization_mode", "WORKSPACE_WRITE").Error)
	require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, "sqlite", 11), "downgrade must refuse to weaken existing Agent config")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		a, ok, e := m.ByPrincipalOn(t.Context(), tx, 7, 101)
		require.NoError(t, e)
		require.True(t, ok)
		require.Equal(t, "WORKSPACE_WRITE", a.AuthorizationMode)
		require.Len(t, a.Roles, 2)
		return nil
	}))
	require.NoError(t, db.Table(m.names.Agents.String()).Where("id=?", 1).Update("authorization_mode", "READ_ONLY").Error)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, "sqlite", 11))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, "sqlite", 12))
}
