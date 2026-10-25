-- +goose Up
CREATE TABLE {{.Prefix}}execution_executions (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL,
 assignment_id INTEGER NOT NULL,
 machine_id INTEGER NOT NULL,
 initiator_id INTEGER NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('CREATED','RUNNING','PAUSED','INTERRUPTED','SUCCEEDED','FAILED','CANCELLED')),
 state_reason VARCHAR(64) NOT NULL,
 result_state VARCHAR(32) NOT NULL CHECK(result_state IN ('NONE','UNKNOWN','CONFIRMED')),
 plan_json TEXT NOT NULL,
 plan_hash CHAR(64) NOT NULL,
 dispatch_epoch BIGINT NOT NULL CHECK(dispatch_epoch>0),
 last_seq BIGINT NOT NULL CHECK(last_seq>=0),
 lease_expires_at DATETIME,
 cancel_requested_at DATETIME,
 version INTEGER NOT NULL CHECK(version>0),
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 UNIQUE(tenant_id,id)
);
CREATE INDEX {{.Prefix}}execution_machine_dispatch ON {{.Prefix}}execution_executions(tenant_id,machine_id,state,id);
CREATE TABLE {{.Prefix}}execution_receipts (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 execution_id INTEGER NOT NULL,
 dispatch_epoch BIGINT NOT NULL,
 seq BIGINT NOT NULL CHECK(seq>0),
 kind VARCHAR(32) NOT NULL,
 content_hash CHAR(64) NOT NULL,
 state VARCHAR(32) NOT NULL,
 created_at DATETIME NOT NULL,
 UNIQUE(tenant_id,execution_id,dispatch_epoch,seq),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE TABLE {{.Prefix}}execution_logs (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 execution_id INTEGER NOT NULL,
 dispatch_epoch BIGINT NOT NULL,
 seq BIGINT NOT NULL CHECK(seq>0),
 content TEXT NOT NULL,
 content_hash CHAR(64) NOT NULL,
 created_at DATETIME NOT NULL,
 UNIQUE(tenant_id,execution_id,dispatch_epoch,seq),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
CREATE TABLE {{.Prefix}}execution_evidence (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 execution_id INTEGER NOT NULL,
 kind VARCHAR(32) NOT NULL,
 title VARCHAR(200) NOT NULL,
 content TEXT NOT NULL,
 content_hash CHAR(64) NOT NULL,
 created_at DATETIME NOT NULL,
 UNIQUE(tenant_id,execution_id,content_hash),
 FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id)
);
-- +goose Down
DROP TABLE IF EXISTS {{.Prefix}}execution_evidence;
DROP TABLE IF EXISTS {{.Prefix}}execution_logs;
DROP TABLE IF EXISTS {{.Prefix}}execution_receipts;
DROP TABLE IF EXISTS {{.Prefix}}execution_executions;
