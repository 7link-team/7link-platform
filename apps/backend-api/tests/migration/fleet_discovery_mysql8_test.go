package migration_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/fleet/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"time"
)

func TestFleetWorkdirLifecycleAcrossEngines(t *testing.T) {
	for _, engine := range append([]databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}, realMySQLEngines(t)...) {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&fleet.Module{}).MigrationSource()
			source.Prefix = deploymentPrefix + "workdir_"
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 19))
			db := openMilestoneGORM(t, raw, engine.dialect)
			at := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			machineTable := source.Prefix + (model.Machine{}).TableName()
			key := "workdir-machine"
			require.NoError(t, db.Table(machineTable).Create(&model.Machine{ID: 1, TenantID: 7, PrincipalID: 101, Name: key, NameKey: &key, State: "OFFLINE", OS: "LINUX", SlotCount: 1, Version: 1, CreatedAt: at, UpdatedAt: at}).Error)
			table := source.Prefix + (model.Workdir{}).TableName()
			// Insert a legacy row without either new column, then verify the upgrade.
			require.NoError(t, db.Table(table).Create(map[string]any{"id": 2, "tenant_id": 7, "machine_id": 1, "project_id": 10, "repository_id": 11, "path": "/repo", "path_key": "legacy-path-key", "created_at": at}).Error)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Workdir{}, table)
			var row model.Workdir
			require.NoError(t, db.Table(table).Where("id=?", 2).First(&row).Error)
			require.Equal(t, 1, row.Version)
			require.Nil(t, row.ArchivedAt)
			require.Equal(t, "/repo", row.Path)
			require.NoError(t, db.Table(table).Where("id=? AND version=?", 2, 1).Updates(map[string]any{"version": 2, "archived_at": at}).Error)
			require.NoError(t, db.Table(table).Where("id=?", 2).First(&row).Error)
			require.Equal(t, 2, row.Version)
			require.NotNil(t, row.ArchivedAt)
			require.True(t, at.Equal(*row.ArchivedAt))
			result := db.Table(table).Where("id=? AND version=?", 2, 1).Update("path", "/stale")
			require.NoError(t, result.Error)
			require.Zero(t, result.RowsAffected)
			require.NoError(t, db.Table(table).Where("id=?", 2).Update("archived_at", nil).Error)
			row = model.Workdir{} // GORM does not clear an existing pointer for a NULL column.
			require.NoError(t, db.Table(table).Where("id=?", 2).First(&row).Error)
			require.Nil(t, row.ArchivedAt)
		})
	}
}

func TestFleetDiscoveryAndGatewayLargePayloadOnRealMySQL8(t *testing.T) {
	dsn := os.Getenv("MYSQL8_DSN")
	if dsn == "" {
		t.Skip("requires isolated MYSQL8_DSN")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	verifyRealMySQLServer(t, db)
	source := (&fleet.Module{}).MigrationSource()
	source.Prefix = "catalog_"
	require.NoError(t, goosex.Migrate(t.Context(), db, source, "mysql"))
	_, err = db.Exec(`INSERT INTO catalog_fleet_machines
	 (id,tenant_id,principal_id,name,name_key,state,drain_requested,os,slot_count,conn_epoch,version,created_at,updated_at)
	 VALUES (1,7,101,'machine','machine','ONLINE',false,'LINUX',1,1,1,NOW(3),NOW(3))`)
	require.NoError(t, err)
	// Exercise the expression default on a real server before storing large JSON.
	_, err = db.Exec(`INSERT INTO catalog_fleet_inspections
	 (id,tenant_id,machine_id,requested_by,kind,state,reason_code,session_id,conn_epoch,result_hash,expires_at,created_at,updated_at)
	 VALUES (2,7,1,11,'MODELS','READY','',3,1,'',NOW(3),NOW(3),NOW(3))`)
	require.NoError(t, err)
	var actual string
	require.NoError(t, db.QueryRow("SELECT result_json FROM catalog_fleet_inspections WHERE id=2").Scan(&actual))
	require.Equal(t, "[]", actual)
	models := make([]string, 1000)
	for i := range models {
		models[i] = fmt.Sprintf("%04d%s", i, strings.Repeat("x", 156))
	}
	large, err := json.Marshal(models)
	require.NoError(t, err)
	require.Greater(t, len(large), 65535)
	_, err = db.Exec("UPDATE catalog_fleet_inspections SET result_json=? WHERE id=2", string(large))
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT result_json FROM catalog_fleet_inspections WHERE id=2").Scan(&actual))
	require.Equal(t, string(large), actual)
	_, err = db.Exec(`INSERT INTO catalog_fleet_machine_runtimes
	 (id,tenant_id,machine_id,kind,version,executable_path,models_json,inspected_at)
	 VALUES (4,7,1,'CODEX','1','/runtime',?,NOW(3))`, string(large))
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT models_json FROM catalog_fleet_machine_runtimes WHERE id=4").Scan(&actual))
	require.Equal(t, string(large), actual)
	_, err = db.Exec(`INSERT INTO catalog_fleet_gateway_configs
	 (id,tenant_id,enabled,default_model,records_json,version,revision,updated_at)
	 VALUES (5,7,false,'',?,1,1,NOW(3))`, string(large))
	require.NoError(t, err)
	require.NoError(t, db.QueryRow("SELECT records_json FROM catalog_fleet_gateway_configs WHERE id=5").Scan(&actual))
	require.Equal(t, string(large), actual)
}
