package migration_test

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/webhook"
	"7link.com/platform/internal/iam/webhook/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestWebhookMetadataPopulatedUpgradeAndSafeRollbackAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&webhook.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "callback_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 1))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.Subscription{}).TableName()
			at := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 1, "tenant_id": 7, "name": "Existing receiver", "url": "https://receiver.example.test", "secret_ciphertext": "sealed", "event_types": "", "status": "active", "after_sequence": 17, "created_at": at, "updated_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var row model.Subscription
			require.NoError(t, db.Table(table).Where("id=1").Take(&row).Error)
			require.Equal(t, webhook.PayloadAudit, row.PayloadMode)
			require.EqualValues(t, 17, row.AfterSequence)
			require.Error(t, db.Table(table).Where("id=1").Update("payload_mode", "private").Error)
			require.NoError(t, db.Table(table).Where("id=1").Update("payload_mode", webhook.PayloadIntakeMetadata).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 1), "rollback must not silently turn metadata callbacks into full audit subscribers")
			require.NoError(t, db.Table(table).Where("id=1").Take(&row).Error)
			require.Equal(t, webhook.PayloadIntakeMetadata, row.PayloadMode)
			require.NoError(t, db.Table(table).Where("id=1").Update("payload_mode", webhook.PayloadAudit).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 1))
		})
	}
}
