package federation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDisabledFederationRequiresRetainedLegacyQuarantine(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		name := prefix
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			env := newFederationEnvironment(t, prefix)
			table := prefix + "iam_identity_links"
			require.NoError(t, env.db.Migrator().DropTable(table))
			require.NoError(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), "true table absence is safe")
			require.NoError(t, env.db.Exec("CREATE TABLE "+table+" (principal_id BIGINT)").Error)
			require.NoError(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), "empty legacy schema is safe")
			require.NoError(t, env.db.Exec("INSERT INTO "+table+" (principal_id) VALUES (11)").Error)
			require.ErrorIs(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), ErrLegacyQuarantineRequired)
			require.NoError(t, env.db.Exec("ALTER TABLE "+table+" ADD COLUMN binding_provenance TEXT NOT NULL DEFAULT 'unknown'").Error)
			require.NoError(t, env.db.Exec("ALTER TABLE "+table+" ADD COLUMN quarantined_at DATETIME").Error)
			require.NoError(t, env.db.Exec("ALTER TABLE "+table+" ADD COLUMN recovery_evidence TEXT NOT NULL DEFAULT ''").Error)
			require.ErrorIs(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), ErrLegacyQuarantineRequired)
			require.NoError(t, env.db.Exec("UPDATE "+table+" SET quarantined_at = CURRENT_TIMESTAMP").Error)
			require.ErrorIs(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), ErrLegacyQuarantineRequired, "disabled federation cannot bypass the legacy mailbox ownership gate")
			require.NoError(t, env.db.Exec("UPDATE "+table+" SET recovery_evidence = '   '").Error)
			require.ErrorIs(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), ErrLegacyQuarantineRequired)
			require.NoError(t, env.db.Exec("UPDATE "+table+" SET recovery_evidence = 'reviewed independent mailbox ownership'").Error)
			require.NoError(t, RequireLegacyQuarantine(t.Context(), env.db, prefix))
			require.NoError(t, env.db.Exec("INSERT INTO "+table+" VALUES (12, 'new_principal', NULL, '')").Error)
			require.NoError(t, RequireLegacyQuarantine(t.Context(), env.db, prefix), "known new principal links require no legacy invalidation")
		})
	}
}

func TestDisabledFederationQuarantineInspectionFailsClosed(t *testing.T) {
	env := newFederationEnvironment(t)
	sqlDB, err := env.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	require.Error(t, RequireLegacyQuarantine(t.Context(), env.db, ""), "database failure cannot appear as no retained records")
	require.ErrorIs(t, RequireLegacyQuarantine(t.Context(), nil, ""), ErrUnavailable)
}
