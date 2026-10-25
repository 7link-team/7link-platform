-- +goose Up
CREATE TABLE {{.Prefix}}fleet_reset_commands (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 agent_principal_id INTEGER NOT NULL,
 machine_id INTEGER NOT NULL,
 session_id INTEGER NOT NULL,
 conn_epoch BIGINT NOT NULL CHECK(conn_epoch > 0),
 session_epoch INTEGER NOT NULL CHECK(session_epoch > 0),
 workspace_epoch INTEGER NOT NULL CHECK(workspace_epoch > 0),
 kind VARCHAR(16) NOT NULL CHECK(kind IN ('RESTART','SESSION','FULL')),
 state VARCHAR(16) NOT NULL CHECK(state IN ('QUEUED','RUNNING','DONE','FAILED','TIMED_OUT')),
 reason_code VARCHAR(64) NOT NULL,
 created_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL,
 FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);
CREATE INDEX {{.Prefix}}fleet_reset_pending ON {{.Prefix}}fleet_reset_commands(tenant_id,machine_id,state,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}fleet_reset_commands;
