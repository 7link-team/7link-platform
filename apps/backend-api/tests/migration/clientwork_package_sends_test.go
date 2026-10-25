package migration_test

import (
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestClientworkPackageSendsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&clientwork.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "pkgsend_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 30))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			parent := model.DeliveryPackage{TenantID: 7, ID: 1, ProjectID: 3, Title: "Existing publication", ReleaseNotes: "Frozen", Manifest: "{}", ContentHash: strings.Repeat("a", 64), State: "PUBLISHED", RecipientID: 4, ActorID: 2, Version: 2, CreatedAt: at, UpdatedAt: at}
			require.NoError(t, db.Table(source.Prefix+parent.TableName()).Create(&parent).Error)
			table := source.Prefix + (model.PackageSend{}).TableName()
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 31))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.PackageSend{}, table)
				require.Equal(t, 0, tableRowCount(t, raw, table), "migration must not fabricate sends for existing publications")
				message := guid.ID(12)
				failure := "CHANNEL_TRANSPORT_UNAVAILABLE"
				failed := model.PackageSend{TenantID: 7, ID: 10, ProjectID: 3, PackageID: 1, PackageVersion: 2, ContentHash: parent.ContentHash, RecipientID: 4, ChannelID: 8, Attempt: 1, State: "FAILED", FailureCode: &failure, ActorID: 2, CreatedAt: at}
				require.NoError(t, db.Table(table).Create(&failed).Error)
				duplicate := failed
				duplicate.ID = 11
				require.Error(t, db.Table(table).Create(&duplicate).Error, "one outcome per recipient attempt")
				sent := failed
				sent.ID = 11
				sent.Attempt = 2
				sent.State = "SUCCEEDED"
				sent.MessageID = &message
				sent.FailureCode = nil
				require.NoError(t, db.Table(table).Create(&sent).Error)
				var stored model.PackageSend
				require.NoError(t, db.Table(table).Where("id=?", 11).Take(&stored).Error)
				require.Equal(t, parent.ContentHash, stored.ContentHash)
				require.Equal(t, message, *stored.MessageID)
				require.True(t, at.Equal(stored.CreatedAt))
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 30), "persisted send history prevents destructive downgrade")
				require.Equal(t, 2, tableRowCount(t, raw, table))
				require.NoError(t, db.Table(table).Where("tenant_id=?", 7).Delete(&model.PackageSend{}).Error)
				require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 30))
				require.Equal(t, 1, tableRowCount(t, raw, source.Prefix+parent.TableName()))
			}
		})
	}
}
