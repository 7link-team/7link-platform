-- +goose Up
CREATE TABLE {{.Prefix}}execution_verification_tasks (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL,
 result_id BIGINT NOT NULL,
 criterion_id BIGINT NOT NULL,
 result_version INTEGER NOT NULL,
 machine_id BIGINT NOT NULL,
 session_id BIGINT NOT NULL,
 conn_epoch BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL,
 plan_json TEXT NOT NULL,
 plan_hash CHAR(64) NOT NULL,
 observation_json TEXT NOT NULL,
 receipt_hash VARCHAR(64) NOT NULL,
 lease_expires_at DATETIME(3),
 created_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,id),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE INDEX {{.Prefix}}execution_verification_dispatch ON {{.Prefix}}execution_verification_tasks(tenant_id,machine_id,state,id);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}execution_verification_tasks;
