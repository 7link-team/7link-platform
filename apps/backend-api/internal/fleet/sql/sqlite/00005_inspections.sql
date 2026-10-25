-- +goose Up
CREATE TABLE {{.Prefix}}fleet_inspections (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 machine_id INTEGER NOT NULL,
 requested_by INTEGER NOT NULL,
 kind VARCHAR(32) NOT NULL CHECK(kind IN ('RUNTIMES','MODELS','CAPABILITIES')),
 state VARCHAR(32) NOT NULL CHECK(state IN ('QUEUED','RUNNING','READY','ERROR')),
 reason_code VARCHAR(32) NOT NULL,
 active_key INTEGER NULL CHECK(active_key = 1),
 session_id INTEGER NOT NULL,
 conn_epoch BIGINT NOT NULL CHECK(conn_epoch > 0),
 result_hash VARCHAR(64) NOT NULL,
 expires_at DATETIME NOT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 UNIQUE(tenant_id,machine_id,kind,active_key),
 FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_inspections_due ON {{.Prefix}}fleet_inspections(tenant_id,state,id);
CREATE INDEX {{.Prefix}}fleet_inspections_history ON {{.Prefix}}fleet_inspections(tenant_id,machine_id,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_inspections;
