-- +goose Up
CREATE TABLE {{.Prefix}}execution_merges (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL, machine_id BIGINT NOT NULL, actor_id BIGINT NOT NULL,
 session_id BIGINT NOT NULL DEFAULT 0, conn_epoch BIGINT NOT NULL DEFAULT 0,
 attempt INT NOT NULL CHECK (attempt > 0), active_execution_id BIGINT NULL,
 state VARCHAR(16) NOT NULL CHECK (state IN ('PENDING','LEASED','MERGED','CONFLICT','FAILED')),
 authority_source VARCHAR(200) NOT NULL, actor_kind VARCHAR(16) NOT NULL CHECK (actor_kind IN ('HUMAN','AGENT')),
 reason TEXT NOT NULL, plan_json TEXT NOT NULL, plan_hash VARCHAR(64) NOT NULL,
 observation_json TEXT NOT NULL, receipt_hash VARCHAR(64) NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL, deadline_at TIMESTAMPTZ(3) NOT NULL, finished_at TIMESTAMPTZ(3) NULL,
 CONSTRAINT {{.Prefix}}exec_merge_execution_fk FOREIGN KEY (tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id),
 CONSTRAINT {{.Prefix}}exec_merge_attempt UNIQUE(tenant_id,execution_id,attempt),
 CONSTRAINT {{.Prefix}}exec_merge_active UNIQUE(tenant_id,active_execution_id),
 CONSTRAINT {{.Prefix}}exec_merge_state CHECK ((state IN ('PENDING','LEASED','MERGED') AND active_execution_id IS NOT NULL AND active_execution_id = execution_id) OR (state IN ('CONFLICT','FAILED') AND active_execution_id IS NULL)),
 CONSTRAINT {{.Prefix}}exec_merge_finished CHECK ((state IN ('PENDING','LEASED') AND finished_at IS NULL) OR (state IN ('MERGED','CONFLICT','FAILED') AND finished_at IS NOT NULL))
);
CREATE INDEX {{.Prefix}}exec_merge_machine ON {{.Prefix}}execution_merges(tenant_id,machine_id,state,id);
-- +goose Down
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}exec_merge_rollback_guard (n INT NOT NULL CHECK(n = 0));
INSERT INTO {{.Prefix}}exec_merge_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_merges;
DROP TABLE {{.Prefix}}exec_merge_rollback_guard;
DROP TABLE {{.Prefix}}execution_merges;
