-- +goose Up
CREATE TABLE {{.Prefix}}execution_preview_sessions (
 id BIGINT NOT NULL PRIMARY KEY,tenant_id BIGINT NOT NULL,project_id BIGINT NOT NULL,execution_id BIGINT NOT NULL,
 machine_id BIGINT NOT NULL,runner_session_id BIGINT NOT NULL,conn_epoch BIGINT NOT NULL,dispatch_epoch BIGINT NOT NULL,
 port INTEGER NOT NULL,label VARCHAR(26) NOT NULL,state VARCHAR(16) NOT NULL,stop_reason VARCHAR(32) NOT NULL,
 started_by BIGINT NOT NULL,created_at DATETIME(3) NOT NULL,last_access_at DATETIME(3) NOT NULL,
 expires_at DATETIME(3) NOT NULL,ended_at DATETIME(3) NULL,version INTEGER NOT NULL,
 CONSTRAINT {{.Prefix}}ck_preview_port CHECK(port>=1 AND port<=65535),
 CONSTRAINT {{.Prefix}}ck_preview_version CHECK(version>0)
);
CREATE UNIQUE INDEX {{.Prefix}}uq_execution_preview_label ON {{.Prefix}}execution_preview_sessions(label);
CREATE INDEX {{.Prefix}}idx_execution_preview_active ON {{.Prefix}}execution_preview_sessions(tenant_id,execution_id,state);
CREATE INDEX {{.Prefix}}idx_execution_preview_idle ON {{.Prefix}}execution_preview_sessions(state,last_access_at);
CREATE TABLE {{.Prefix}}execution_preview_access (
 id BIGINT NOT NULL PRIMARY KEY,session_id BIGINT NOT NULL,principal_id BIGINT NOT NULL,code_hash VARCHAR(64) NOT NULL,
 code_expires_at DATETIME(3) NOT NULL,consumed_at DATETIME(3) NULL,cookie_hash VARCHAR(64) NULL,
 cookie_expires_at DATETIME(3) NULL,return_path VARCHAR(2000) NOT NULL
);
CREATE UNIQUE INDEX {{.Prefix}}uq_execution_preview_code ON {{.Prefix}}execution_preview_access(code_hash);
CREATE UNIQUE INDEX {{.Prefix}}uq_execution_preview_cookie ON {{.Prefix}}execution_preview_access(cookie_hash);
CREATE INDEX {{.Prefix}}idx_execution_preview_access_session ON {{.Prefix}}execution_preview_access(session_id);
-- +goose Down
DROP TABLE {{.Prefix}}execution_preview_access;
DROP TABLE {{.Prefix}}execution_preview_sessions;
