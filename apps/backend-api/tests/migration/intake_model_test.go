package migration_test

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/intake/model"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
			db := openMilestoneGORM(t, raw, engine.dialect)
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
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
		})
	}
}
