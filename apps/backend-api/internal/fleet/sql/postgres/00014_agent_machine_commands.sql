-- +goose Up
CREATE TABLE {{.Prefix}}fleet_agent_machine_commands (
 id BIGINT NOT NULL PRIMARY KEY,tenant_id BIGINT NOT NULL,agent_principal_id BIGINT NOT NULL,machine_id BIGINT NOT NULL,credential_id BIGINT NULL,config_revision INTEGER NOT NULL,
 kind VARCHAR(32) NOT NULL,state VARCHAR(16) NOT NULL,sealed_credential TEXT NOT NULL,session_id BIGINT NULL,conn_epoch BIGINT NOT NULL,reason_code VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,expires_at TIMESTAMPTZ NOT NULL,completed_at TIMESTAMPTZ NULL
);
CREATE INDEX {{.Prefix}}idx_agent_machine_commands ON {{.Prefix}}fleet_agent_machine_commands(tenant_id,machine_id,state,id);
-- +goose Down
DROP TABLE {{.Prefix}}fleet_agent_machine_commands;
