-- +goose Up
CREATE TABLE {{.Prefix}}execution_executions (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 assignment_id BIGINT NOT NULL,
 machine_id BIGINT NOT NULL,
 initiator_id BIGINT NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('CREATED','RUNNING','PAUSED','INTERRUPTED','SUCCEEDED','FAILED','CANCELLED')),
 state_reason VARCHAR(64) NOT NULL,
 result_state VARCHAR(32) NOT NULL CHECK(result_state IN ('NONE','UNKNOWN','CONFIRMED')),
 plan_json TEXT NOT NULL,
 plan_hash CHAR(64) NOT NULL,
 dispatch_epoch BIGINT NOT NULL CHECK(dispatch_epoch>0),
 last_seq BIGINT NOT NULL CHECK(last_seq>=0),
 lease_expires_at DATETIME(3),
 cancel_requested_at DATETIME(3),
 version INTEGER NOT NULL CHECK(version>0),
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,id)
);
CREATE INDEX {{.Prefix}}execution_machine_dispatch ON {{.Prefix}}execution_executions(tenant_id,machine_id,state,id);
CREATE TABLE {{.Prefix}}execution_receipts (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL,
 dispatch_epoch BIGINT NOT NULL,
 seq BIGINT NOT NULL CHECK(seq>0),
 kind VARCHAR(32) NOT NULL,
 content_hash CHAR(64) NOT NULL,
 state VARCHAR(32) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,execution_id,dispatch_epoch,seq),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE TABLE {{.Prefix}}execution_logs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL,
 dispatch_epoch BIGINT NOT NULL,
 seq BIGINT NOT NULL CHECK(seq>0),
 content TEXT NOT NULL,
 content_hash CHAR(64) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,execution_id,dispatch_epoch,seq),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE TABLE {{.Prefix}}execution_evidence (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL,
 kind VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL,
 content TEXT NOT NULL,
 content_hash CHAR(64) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 UNIQUE(tenant_id,execution_id,content_hash),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}execution_evidence;
DROP TABLE IF EXISTS {{.Prefix}}execution_logs;
DROP TABLE IF EXISTS {{.Prefix}}execution_receipts;
DROP TABLE IF EXISTS {{.Prefix}}execution_executions;
