package migration_test

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"database/sql"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIntakeModelsAndReferencesAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&intake.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			table := deploymentPrefix + "intake_contacts"
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Contact{}, table)
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Record{}, deploymentPrefix+"intake_records")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SignedBrief{}, deploymentPrefix+"intake_signed_briefs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.TriageCard{}, deploymentPrefix+"intake_triage_cards")
			db := openMilestoneGORM(t, raw, engine.dialect)
			// Generic assessments have no canonical change ID. Existing CHANGE
			// associations and per-source immutable revision uniqueness survive.
			cards := deploymentPrefix + "intake_triage_cards"
			change := guid.ID(800)
			card := model.TriageCard{ID: 700, TenantID: 1, IntakeID: 100, Revision: 1, ProposalHash: strings.Repeat("a", 64), ProposalJSON: `{"summary":"Generic assessment"}`, ImpactJSON: "{}", ActorID: 2, CreatedAt: time.Now().UTC()}
			require.NoError(t, db.Table(cards).Create(&card).Error)
			card.ID = 701
			require.Error(t, db.Table(cards).Create(&card).Error, "same source revision cannot be overwritten")
			card.Revision, card.ChangeID = 2, &change
			require.NoError(t, db.Table(cards).Create(&card).Error)
			var generic model.TriageCard
			require.NoError(t, db.Table(cards).Where("id=?", 700).Take(&generic).Error)
			require.Nil(t, generic.ChangeID)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 4))
			var preserved model.TriageCard
			require.NoError(t, db.Table(cards).Where("id=?", 701).Take(&preserved).Error)
			require.Equal(t, change, *preserved.ChangeID)
			require.Error(t, db.Table(cards).Where("id=?", 700).Take(&generic).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			briefs := deploymentPrefix + "intake_signed_briefs"
			signedAt := time.Date(2026, 10, 20, 1, 2, 3, 456000000, time.UTC)
			sheet := model.SignedBrief{ID: 500, TenantID: 1, ProjectID: 20, IntakeID: 10, BindingID: 30, ItemsJSON: `[{"id":"one","text":"Signed scope"}]`, SHA256: strings.Repeat("a", 64), SignedBy: 2, SignedAt: signedAt}
			require.NoError(t, db.Table(briefs).Create(&sheet).Error)
			var stored model.SignedBrief
			require.NoError(t, db.Table(briefs).Where("id=?", 500).Take(&stored).Error)
			require.True(t, signedAt.Equal(stored.SignedAt))
			sheet.ID = 501
			require.Error(t, db.Table(briefs).Create(&sheet).Error, "a binding has exactly one immutable sheet")
			sheet.TenantID = 2
			require.NoError(t, db.Table(briefs).Create(&sheet).Error)

			insert := func(id, tenant int, email any, principal any) error {
				return db.Table(table).Create(map[string]any{"id": id, "tenant_id": tenant, "display_name": "Contact", "email": email, "principal_id": principal, "created_at": time.Now(), "updated_at": time.Now()}).Error
			}
			require.NoError(t, insert(1, 1, nil, nil))
			require.NoError(t, insert(2, 1, nil, nil))
			require.NoError(t, insert(3, 1, "person@example.test", 30))
			require.Error(t, insert(4, 1, "person@example.test", 40))
			require.Error(t, insert(5, 1, "other@example.test", 30))
			require.NoError(t, insert(6, 2, "person@example.test", 30))
			records := deploymentPrefix + "intake_records"
			insertRecord := func(id int, changes map[string]any) error {
				row := map[string]any{"id": id, "tenant_id": 1, "ref_no": id, "channel_id": 7, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Source", "raw_content_hash": strings.Repeat("a", 64), "received_at": time.Now(), "reply_token": fmt.Sprintf("reply-%d", id), "created_at": time.Now(), "updated_at": time.Now()}
				for key, value := range changes {
					row[key] = value
				}
				return db.Table(records).Create(row).Error
			}
			require.NoError(t, insertRecord(10, nil))
			require.NoError(t, insertRecord(11, map[string]any{"external_ref": "EXT", "origin_acceptance_id": 99}))
			require.Error(t, insertRecord(12, map[string]any{"external_ref": "EXT"}))
			require.Error(t, insertRecord(13, map[string]any{"origin_acceptance_id": 99}))
			require.Error(t, insertRecord(14, map[string]any{"ref_no": 10}))
			require.Error(t, insertRecord(15, map[string]any{"reply_token": "reply-10"}))
			require.NoError(t, insertRecord(16, map[string]any{"tenant_id": 2, "ref_no": 10, "external_ref": "EXT", "origin_acceptance_id": 99}))
			if engine.dialect != "mysql" {
				for _, bad := range []map[string]any{{"status": "CONVERTED"}, {"status": "MERGED"}, {"status": "MERGED", "merged_into_intake_id": 20}, {"status": "DECLINED"}, {"kind": "CHANGE"}, {"change_of_intake_id": 10}, {"kind": "CHANGE", "change_of_intake_id": 20, "target_requirement_id": 3}, {"channel_kind": "PORTAL"}, {"raw_content": strings.Repeat("a", 65537)}} {
					require.Error(t, insertRecord(20, bad))
				}
				require.NoError(t, insertRecord(21, map[string]any{"kind": "CHANGE", "change_of_intake_id": 10, "target_requirement_id": 3}))

				require.Error(t, db.Table(table).Where("id = ?", 1).Update("state", "UNKNOWN").Error)
				require.Error(t, db.Table(table).Where("id = ?", 1).Update("kind", "AGENT").Error)
				require.NoError(t, db.Table(table).Where("id = ?", 1).Update("archived_at", time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)).Error)
				reader := intake.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix(deploymentPrefix)), intake.Config{})
				abort := errors.New("rollback contact state")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					ref, found, err := reader.GetOn(t.Context(), tx, 1, 10)
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, "RECEIVED", ref.Status)
					require.Nil(t, ref.ProjectID)
					_, found, err = reader.GetOn(t.Context(), tx, 2, 10)
					require.NoError(t, err)
					require.False(t, found)
					require.NoError(t, tx.Table(records).Where("id = ?", 10).Update("project_id", 30).Error)
					ref, found, err = reader.GetOn(t.Context(), tx, 1, 10)
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, guid.ID(30), *ref.ProjectID)
					refs, err := reader.ContactsOn(t.Context(), tx, 1, []guid.ID{6, 3, 2, 2})
					require.NoError(t, err)
					require.Len(t, refs, 2)
					require.Equal(t, guid.ID(2), refs[0].ID)
					require.Equal(t, guid.ID(3), refs[1].ID)
					require.NoError(t, tx.Table(table).Where("id = ?", 2).Update("state", "BLOCKED").Error)
					refs, err = reader.ContactsOn(t.Context(), tx, 1, []guid.ID{2})
					require.NoError(t, err)
					require.Equal(t, "BLOCKED", refs[0].State)
					return abort
				}), abort)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					ref, found, err := reader.GetOn(t.Context(), tx, 1, 10)
					require.NoError(t, err)
					require.True(t, found)
					require.Nil(t, ref.ProjectID)
					refs, err := reader.ContactsOn(t.Context(), tx, 1, []guid.ID{2})
					require.NoError(t, err)
					require.Equal(t, "ACTIVE", refs[0].State)
					return nil
				}))

			}
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 2))
			require.False(t, db.Migrator().HasTable(briefs))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SignedBrief{}, briefs)
		})
	}
}

func TestIntakeDispositionDeliveryAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run the isolated real MySQL8 gate")
		}
		raw, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&intake.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			if engine.name == "mysql8" {
				source.Prefix = "regression_delivery_"
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + "intake_records"
			now := time.Now().UTC().Truncate(time.Millisecond)
			content := "Original immutable request: preserved across receipt migrations"
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 10, "tenant_id": 1, "ref_no": 10, "channel_id": 7, "channel_kind": "API", "contact_id": 1, "title": "Original", "raw_content": content, "raw_content_hash": strings.Repeat("a", 64), "reply_token": "delivery-upgrade", "received_at": now, "created_at": now, "updated_at": now}).Error)
			assertPreserved := func() {
				t.Helper()
				var row model.Record
				require.NoError(t, db.Table(table).Where("id=10").Take(&row).Error)
				require.NotNil(t, row.RawContent)
				require.Equal(t, content, *row.RawContent)
				require.Equal(t, strings.Repeat("a", 64), row.RawContentHash)
				require.Equal(t, "NOT_REQUESTED", row.ClientNotificationState)
				require.Nil(t, row.ClientNotificationMessageID)
			}
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
				assertPreserved()
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.Record{}, table)
				for _, state := range []string{"UNAVAILABLE", "POSTED"} {
					var message any
					if state == "POSTED" {
						message = 99
					}
					require.NoError(t, db.Table(table).Where("id=10").Updates(map[string]any{"client_notification_state": state, "client_notification_message_id": message}).Error)
					var row model.Record
					require.NoError(t, db.Table(table).Where("id=10").Take(&row).Error)
					require.Equal(t, state, row.ClientNotificationState)
					if state == "POSTED" {
						require.NotNil(t, row.ClientNotificationMessageID)
						require.EqualValues(t, 99, *row.ClientNotificationMessageID)
					} else {
						require.Nil(t, row.ClientNotificationMessageID)
					}
				}
				if engine.name != "mysql" { // Embedded compatibility engine ignores CHECK constraints.
					for _, bad := range []map[string]any{{"client_notification_state": "READ", "client_notification_message_id": 99}, {"client_notification_state": "POSTED", "client_notification_message_id": nil}, {"client_notification_state": "POSTED", "client_notification_message_id": 0}, {"client_notification_state": "POSTED", "client_notification_message_id": -1}, {"client_notification_state": "UNAVAILABLE", "client_notification_message_id": 99}, {"client_notification_state": "NOT_REQUESTED", "client_notification_message_id": 99}} {
						require.Error(t, db.Table(table).Where("id=10").Updates(bad).Error)
					}
				}
				require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 7))
				var preserved string
				require.NoError(t, db.Table(table).Select("raw_content").Where("id=10").Scan(&preserved).Error)
				require.Equal(t, content, preserved)
			}
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertPreserved()
		})
	}
}

func TestIntakeReworkDispositionAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}, {name: "mysql8", dialect: "mysql", open: func(t *testing.T) *sql.DB {
		dsn := os.Getenv("MYSQL8_DSN")
		if dsn == "" {
			t.Skip("run the isolated real MySQL8 gate")
		}
		raw, err := sql.Open("mysql", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, raw.Close()) })
		verifyRealMySQLServer(t, raw)
		return raw
	}}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&intake.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			if engine.name == "mysql8" {
				source.Prefix = "regression_rework_"
			}
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 5))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := source.Prefix + (model.Record{}).TableName()
			at := time.Now().UTC().Truncate(time.Millisecond)
			legacy := model.Record{ID: 1, RefNo: 1, ChannelID: 7, ChannelKind: "API", ContactID: 2, Kind: "NEW", Status: "RECEIVED", Title: "Preserved source", RawContentFormat: "PLAIN", RawContentHash: strings.Repeat("a", 64), ReplyToken: "rework-legacy", Version: 3, TenantID: 1, ReceivedAt: at, CreatedAt: at, UpdatedAt: at}
			// Freeze the version-5 source shape before later request-timing columns.
			require.NoError(t, db.Table(table).Select("id", "ref_no", "channel_id", "channel_kind", "contact_id", "kind", "status", "title", "raw_content_format", "raw_content_hash", "reply_token", "version", "tenant_id", "received_at", "created_at", "updated_at").Create(&legacy).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var stored model.Record
			require.NoError(t, db.Table(table).Where("id=?", legacy.ID).Take(&stored).Error)
			require.Equal(t, legacy.Title, stored.Title)
			require.Equal(t, legacy.Version, stored.Version)
			require.True(t, at.Equal(stored.ReceivedAt))
			rework := legacy
			rework.ID, rework.RefNo, rework.ReplyToken = 2, 2, "rework-subset"
			rework.Kind, rework.Status = "REWORK", "CONVERTED"
			require.NoError(t, db.Table(table).Create(&rework).Error)
			if engine.name != "mysql" { // Only the embedded compatibility engine does not enforce CHECKs.
				invalid := rework
				invalid.ID, invalid.RefNo, invalid.ReplyToken, invalid.Kind = 3, 3, "new-no-target", "NEW"
				require.Error(t, db.Table(table).Create(&invalid).Error, "NEW conversion must still identify a requirement")
				require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 5), "do not lose multi-target REWORK on rollback")
				stored = model.Record{}
				require.NoError(t, db.Table(table).Where("id=?", rework.ID).Take(&stored).Error)
				require.Equal(t, "CONVERTED", stored.Status)
			}
			require.NoError(t, db.Table(table).Where("id=?", rework.ID).Delete(&model.Record{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 5))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			require.Equal(t, 1, tableRowCount(t, raw, table))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Record{}, table)
		})
	}
}

func TestIntakeTriageRequestTimestampUpgradeAndRollbackAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&intake.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 6))
			db := openMilestoneGORM(t, raw, engine.dialect)
			table := deploymentPrefix + "intake_records"
			at := time.Now().UTC().Truncate(time.Millisecond)
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 100, "tenant_id": 1, "ref_no": 1, "channel_id": 10, "channel_kind": "API", "contact_id": 20, "title": "Existing source", "raw_content_hash": strings.Repeat("a", 64), "reply_token": "existing-source", "received_at": at, "last_team_activity_at": at, "created_at": at, "updated_at": at}).Error)
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			var row model.Record
			require.NoError(t, db.Table(table).Where("id=100").Take(&row).Error)
			require.Nil(t, row.LastTriageRequestedAt)
			require.NotNil(t, row.LastTeamActivityAt)
			requested := at.Add(time.Minute)
			require.NoError(t, db.Table(table).Where("id=100").Update("last_triage_requested_at", requested).Error)
			require.NoError(t, db.Table(table).Where("id=100").Take(&row).Error)
			require.True(t, requested.Equal(*row.LastTriageRequestedAt))
			require.True(t, at.Equal(*row.LastTeamActivityAt))
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 6))
			require.Equal(t, 1, tableRowCount(t, raw, table))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			var after model.Record
			require.NoError(t, db.Table(table).Where("id=100").Take(&after).Error)
			require.Nil(t, after.LastTriageRequestedAt)
			require.True(t, at.Equal(*after.LastTeamActivityAt))
		})
	}
}
