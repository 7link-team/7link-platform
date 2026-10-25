package migration_test

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/governance"
	gm "7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestInboxAttentionPopulatedUpgradeAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&delivery.Module{}).MigrationSource()
			source.Prefix = "inbox_attention_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 35))
			exec := func(sql string) error {
				_, e := raw.ExecContext(t.Context(), strings.ReplaceAll(sql, "@", source.Prefix))
				return e
			}
			require.NoError(t, exec("INSERT INTO @delivery_projects(tenant_id,project_id,code,stage_changed_at,created_at,updated_at) VALUES(1,2,'IA','2040-01-01','2040-01-01','2040-01-01')"))
			require.NoError(t, exec("INSERT INTO @delivery_work_items(tenant_id,id,project_id,number,title,type,baseline_version_no,accountable_principal_id,state_changed_at,created_at,updated_at) VALUES(1,20,2,1,'Work','TASK',0,100,'2040-01-01','2040-01-01','2040-01-01')"))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 1, 0, 0, 0, 123000000, time.UTC)
			claim := model.ResultVersion{TenantID: 1, ID: 21, ProjectID: 2, WorkItemID: 20, Sequence: 1, Summary: "Claim", ActorID: 100, VerificationPlan: "{}", CreatedAt: at}
			require.NoError(t, db.Table(source.Prefix+claim.TableName()).Create(&claim).Error)
			row := model.AttentionSession{TenantID: 1, ID: 22, ProjectID: 2, WorkItemID: 20, ClaimID: &claim.ID, PrincipalID: 101, Surface: "REVIEW_DESK", StartedAt: at, LastHeartbeatAt: at, ActiveMS: 15000, LastSequence: 1}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Omit("InteractionID").Create(&row).Error)
			for cycle := 0; cycle < 2; cycle++ {
				require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 36))
				var stored model.AttentionSession
				require.NoError(t, db.Table(table).Where("id=22").Take(&stored).Error)
				require.NotNil(t, stored.ClaimID)
				require.Equal(t, claim.ID, *stored.ClaimID)
				require.Nil(t, stored.InteractionID)
				require.EqualValues(t, 15000, stored.ActiveMS)
				require.EqualValues(t, 1, stored.LastSequence)
				require.True(t, at.Equal(stored.StartedAt))
				assertModelMatchesNamedTable(t, raw, engine.dialect, model.AttentionSession{}, table)
				if cycle == 0 {
					require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 35))
				}
			}
			interaction := guid.ID(300)
			row.ID = 23
			row.Surface = "INBOX"
			row.ClaimID = nil
			row.InteractionID = &interaction
			require.NoError(t, db.Table(table).Create(&row).Error)
			row.ID = 24
			row.ClaimID = &claim.ID
			require.Error(t, db.Table(table).Create(&row).Error, "claim and interaction are mutually exclusive")
			row.ClaimID = nil
			row.InteractionID = nil
			require.Error(t, db.Table(table).Create(&row).Error, "an actual source is required")
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 35), "downgrade cannot discard inbox attention facts")
			require.Equal(t, 2, tableRowCount(t, raw, table))
		})
	}
}
func TestInboxAcknowledgementRecipientRevisionAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&governance.Module{}).MigrationSource()
			source.Prefix = "inbox_ack_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 12))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 13))
			db := openMilestoneGORM(t, raw, engine.dialect)
			row := gm.InboxAcknowledgement{ID: 1, TenantID: 7, PrincipalID: 8, Kind: "CLARIFICATION_REPLY", SourceID: 9, SourceRevision: strings.Repeat("a", 64), CreatedAt: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC)}
			table := source.Prefix + row.TableName()
			require.NoError(t, db.Table(table).Create(&row).Error)
			row.ID = 2
			require.Error(t, db.Table(table).Create(&row).Error)
			row.PrincipalID = 10
			require.NoError(t, db.Table(table).Create(&row).Error)
			row.ID = 3
			row.SourceRevision = strings.Repeat("b", 64)
			require.NoError(t, db.Table(table).Create(&row).Error)
			row.ID = 4
			row.Kind = "WORK_ITEM_REVIEW"
			require.Error(t, db.Table(table).Create(&row).Error)
			require.Error(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 12))
			require.Equal(t, 3, tableRowCount(t, raw, table))
			require.NoError(t, db.Table(table).Where("tenant_id=7").Delete(&gm.InboxAcknowledgement{}).Error)
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 12))
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 13))
			assertModelMatchesNamedTable(t, raw, engine.dialect, gm.InboxAcknowledgement{}, table)
		})
	}
}
