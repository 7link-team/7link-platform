-- +goose Up
CREATE TABLE {{.Prefix}}execution_approvals (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL,
 dispatch_epoch BIGINT NOT NULL,
 session_id BIGINT NOT NULL,
 conn_epoch BIGINT NOT NULL,
 request_id VARCHAR(160) NOT NULL,
 tool VARCHAR(160) NOT NULL,
 input_json TEXT NOT NULL,
 request_hash CHAR(64) NOT NULL,
 state VARCHAR(32) NOT NULL,
 actor_id BIGINT,
 reason TEXT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 expires_at DATETIME(3) NOT NULL,
 decided_at DATETIME(3),
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,execution_id,dispatch_epoch,request_id),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id),
 CONSTRAINT {{.Prefix}}execution_approval_state CHECK(state IN ('PENDING','ALLOW','DENY')),
 CONSTRAINT {{.Prefix}}execution_approval_decision CHECK((state='PENDING' AND actor_id IS NULL AND decided_at IS NULL) OR (state IN ('ALLOW','DENY') AND actor_id IS NOT NULL AND decided_at IS NOT NULL))
);
CREATE INDEX {{.Prefix}}execution_approval_pending ON {{.Prefix}}execution_approvals(tenant_id,state,expires_at,id);
-- +goose Down
-- Durable permission and Human decision history cannot be erased by rollback.
CREATE TABLE IF NOT EXISTS {{.Prefix}}execution_approval_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}execution_approval_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_approvals;
DROP TABLE {{.Prefix}}execution_approval_rollback_guard;
DROP TABLE IF EXISTS {{.Prefix}}execution_approvals;
