package migration_test

import (
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
)

func TestJobsModelAndConstraintsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite}, {name: "mysql", dialect: "mysql", open: testdb.OpenMySQL}, {name: "postgres", dialect: "postgres", open: testdb.OpenPostgres}} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			source := (&jobs.Module{}).MigrationSource()
			source.Prefix = maximumPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Job{}, maximumPrefix+"background_jobs")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Schedule{}, maximumPrefix+"background_job_schedules")
			db := openMilestoneGORM(t, raw, engine.dialect)
			names := query.BindNames(maximumPrefix)
			jq := query.BindJobs(db, names)
			sq := query.BindSchedules(db, names)
			instant := time.Date(2040, 1, 2, 3, 4, 5, 123000000, time.UTC)
			row := model.Job{ID: 1, Kind: "governance.seed_reconcile", Queue: "default", State: "PENDING", Priority: 5, Payload: "{}", ErrorHistory: "[]", MaxAttempts: 8, TimeoutMS: 300000, CreatedAt: instant, UpdatedAt: instant, Version: 1}
			require.NoError(t, jq.WithContext(t.Context()).Create(&row))
			// NULL idempotency keys must allow separate jobs, including platform jobs.
			row.ID = 2
			require.NoError(t, jq.WithContext(t.Context()).Create(&row))
			key := "once"
			row.ID = 3
			row.IdempotencyKey = &key
			require.NoError(t, jq.WithContext(t.Context()).Create(&row))
			row.ID = 4
			require.Error(t, jq.WithContext(t.Context()).Create(&row))
			row.TenantID = 1
			require.NoError(t, jq.WithContext(t.Context()).Create(&row))
			row.ID = 5
			row.Kind = "governance.other"
			require.NoError(t, jq.WithContext(t.Context()).Create(&row))
			loaded, err := jq.WithContext(t.Context()).Where(jq.ID.Eq(guid.ID(1))).First()
			require.NoError(t, err)
			require.True(t, instant.Equal(loaded.CreatedAt), "timestamp beyond 2038 retains millisecond precision: %v", loaded.CreatedAt)
			require.Nil(t, loaded.LeaseOwner)
			require.Nil(t, loaded.LeaseExpiresAtMS)
			require.Nil(t, loaded.CreatedBy)
			require.Nil(t, loaded.StartedAt)
			require.Nil(t, loaded.FinishedAt)
			for _, change := range []map[string]any{{"state": "UNKNOWN"}, {"priority": 0}, {"priority": 10}, {"attempts": -1}, {"max_attempts": 0}, {"max_attempts": 51}, {"timeout_ms": 999}, {"timeout_ms": 3600001}, {"kind": strings.Repeat("x", 129)}, {"payload": "not json"}, {"error_history": "not json"}} {
				require.Error(t, db.Table(names.Jobs.String()).Where("id = ?", 1).Updates(change).Error, "%v", change)
			}

			require.Nil(t, loaded.FanoutKind)
			require.Nil(t, loaded.FanoutFireMS)
			target, plan, fire := "jobs.target", "jobs.hourly", instant.UnixMilli()
			payload := `{"data":"` + strings.Repeat("x", 64*1024-len(`{"data":""}`)) + `"}`
			require.Len(t, payload, 64*1024)
			fanout := model.Job{ID: 10, Kind: "jobs.fan_out", Queue: "default", State: "PENDING", Priority: 5, Payload: payload, ConcurrencyKey: &plan, FanoutKind: &target, FanoutFireMS: &fire, ErrorHistory: "[]", MaxAttempts: 8, TimeoutMS: 300000, CreatedAt: instant, UpdatedAt: instant, Version: 1}
			require.NoError(t, jq.WithContext(t.Context()).Create(&fanout))
			snapshot, err := jq.WithContext(t.Context()).Where(jq.ID.Eq(guid.ID(10))).First()
			require.NoError(t, err)
			require.JSONEq(t, payload, snapshot.Payload)
			require.Equal(t, target, *snapshot.FanoutKind)
			require.Equal(t, fire, *snapshot.FanoutFireMS)
			for _, change := range []map[string]any{{"fanout_kind": nil}, {"fanout_fire_ms": nil}, {"fanout_kind": ""}, {"fanout_kind": "jobs.fan_out"}, {"fanout_kind": strings.Repeat("x", 129)}, {"fanout_fire_ms": -1}, {"kind": "jobs.target"}, {"tenant_id": 1}, {"concurrency_key": nil}, {"concurrency_key": ""}} {
				require.Error(t, db.Table(names.Jobs.String()).Where("id = ?", 10).Updates(change).Error, "%v", change)
			}
			interval := int64(3600000)
			schedule := model.Schedule{ID: 1, Name: "governance.seed_reconcile", Kind: "governance.seed_reconcile", Queue: "default", TenantScope: "PLATFORM", IntervalMS: &interval, Timezone: "UTC", Payload: "{}", Enabled: true, MisfirePolicy: "COALESCE", DefinitionHash: strings.Repeat("a", 64), CreatedAt: instant, UpdatedAt: instant, Version: 1}
			require.NoError(t, sq.WithContext(t.Context()).Create(&schedule))
			for _, change := range []map[string]any{{"interval_ms": nil}, {"interval_ms": 999}, {"cron": "0 * * * *"}, {"tenant_scope": "UNKNOWN"}, {"misfire_policy": "UNKNOWN"}, {"name": strings.Repeat("x", 129)}, {"payload": "not json"}} {
				require.Error(t, db.Table(names.Schedules.String()).Where("id = ?", 1).Updates(change).Error, "%v", change)
			}
			require.NoError(t, db.Table(names.Schedules.String()).Where("id = ?", 1).Updates(map[string]any{"interval_ms": nil, "cron": "0 * * * *"}).Error)
			stored, err := sq.WithContext(t.Context()).Where(sq.ID.Eq(guid.ID(1))).First()
			require.NoError(t, err)
			require.Nil(t, stored.IntervalMS)
			require.Nil(t, stored.LastJobID)
			require.Nil(t, stored.LastFiredAtMS)
			require.Equal(t, "0 * * * *", *stored.Cron)
			schedule.ID = 2
			require.Error(t, sq.WithContext(t.Context()).Create(&schedule), "schedule names are unique")
			require.NoError(t, goosex.MigrateDownTo(t.Context(), raw, source, engine.dialect, 1))
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			restored, err := query.BindJobs(db, names).WithContext(t.Context()).Where(jq.ID.Eq(guid.ID(10))).First()
			require.NoError(t, err)
			require.JSONEq(t, payload, restored.Payload)
			require.Nil(t, restored.FanoutKind)
			require.Nil(t, restored.FanoutFireMS)

		})
	}
}
