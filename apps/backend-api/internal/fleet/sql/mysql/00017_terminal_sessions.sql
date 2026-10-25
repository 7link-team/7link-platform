-- +goose Up
CREATE TABLE {{.Prefix}}fleet_terminal_access (
 machine_id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, enabled BOOLEAN NOT NULL,
 version INTEGER NOT NULL, updated_at DATETIME(3) NOT NULL
);
CREATE TABLE {{.Prefix}}fleet_terminal_sessions (
 id BIGINT PRIMARY KEY, tenant_id BIGINT NOT NULL, machine_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL, project_id BIGINT NOT NULL, opened_by BIGINT NOT NULL,
 runner_session_id BIGINT NOT NULL, conn_epoch BIGINT NOT NULL, dispatch_epoch BIGINT NOT NULL,
 active_execution BIGINT NULL, state VARCHAR(16) NOT NULL, workspace VARCHAR(2048) NOT NULL,
 sandboxed BOOLEAN NOT NULL, opened_at DATETIME(3) NOT NULL, last_input_at DATETIME(3) NOT NULL,
 expires_at DATETIME(3) NOT NULL, closed_at DATETIME(3) NULL, close_reason VARCHAR(64) NOT NULL,
 recording_chunks LONGTEXT NOT NULL, recording_file_id BIGINT NULL, recording_sha256 VARCHAR(64) NOT NULL
);
CREATE UNIQUE INDEX {{.Prefix}}fleet_terminal_active_execution ON {{.Prefix}}fleet_terminal_sessions (tenant_id, active_execution);
CREATE INDEX {{.Prefix}}fleet_terminal_machine ON {{.Prefix}}fleet_terminal_sessions (tenant_id, machine_id, state);
-- +goose Down
DROP TABLE {{.Prefix}}fleet_terminal_sessions;
DROP TABLE {{.Prefix}}fleet_terminal_access;
