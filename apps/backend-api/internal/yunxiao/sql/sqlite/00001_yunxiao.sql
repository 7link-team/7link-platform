-- +goose Up
CREATE TABLE {{.Prefix}}integration_connections (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 provider VARCHAR(16) NOT NULL,
 name VARCHAR(80) NOT NULL,
 organization_id VARCHAR(64) NOT NULL,
 api_endpoint VARCHAR(200) NOT NULL,
 token_sealed TEXT NOT NULL,
 token_last4 VARCHAR(4) NOT NULL,
 webhook_secret_sealed TEXT NOT NULL,
 state VARCHAR(20) NOT NULL,
 last_checked_at DATETIME,
 last_error VARCHAR(500),
 token_scopes TEXT NOT NULL,
 CONSTRAINT {{.Prefix}}integration_conn_provider_ck CHECK (provider = 'YUNXIAO'),
 CONSTRAINT {{.Prefix}}integration_conn_state_ck CHECK (state IN ('PENDING_CHECK','ACTIVE','ERROR','DISABLED'))
);
CREATE INDEX {{.Prefix}}integration_conn_tenant ON {{.Prefix}}integration_connections (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_project_mappings (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 connection_id BIGINT NOT NULL,
 external_project_id VARCHAR(64) NOT NULL,
 external_project_name VARCHAR(200) NOT NULL,
 project_id BIGINT NOT NULL,
 sync_mode VARCHAR(20) NOT NULL,
 field_masters TEXT NOT NULL,
 codeup_repositories TEXT NOT NULL,
 trusted_pipelines TEXT NOT NULL,
 paused BOOLEAN NOT NULL,
 CONSTRAINT {{.Prefix}}integration_pm_mode_ck CHECK (sync_mode IN ('IMPORT_ONLY','READ_MIRROR','BIDIRECTIONAL'))
);
CREATE INDEX {{.Prefix}}integration_pm_tenant ON {{.Prefix}}integration_project_mappings (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_external_refs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 mapping_id BIGINT NOT NULL,
 external_kind VARCHAR(32) NOT NULL,
 external_id VARCHAR(64) NOT NULL,
 local_kind VARCHAR(32) NOT NULL,
 local_id BIGINT NOT NULL,
 external_updated_at DATETIME,
 local_version INTEGER,
 last_synced_at DATETIME
);
CREATE INDEX {{.Prefix}}integration_ref_tenant ON {{.Prefix}}integration_external_refs (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_state_mappings (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 mapping_id BIGINT NOT NULL,
 external_type VARCHAR(80) NOT NULL,
 external_status_id VARCHAR(64) NOT NULL,
 external_status_name VARCHAR(80) NOT NULL,
 local_work_item_type VARCHAR(24),
 maps_to_requirement BOOLEAN NOT NULL,
 local_state_id BIGINT NOT NULL,
 confidence INTEGER NOT NULL,
 confirmed_by BIGINT,
 confirmed_at DATETIME,
 CONSTRAINT {{.Prefix}}integration_sm_confidence_ck CHECK (confidence BETWEEN 0 AND 100),
 CONSTRAINT {{.Prefix}}integration_sm_kind_ck CHECK ((maps_to_requirement = TRUE AND local_work_item_type IS NULL) OR (maps_to_requirement = FALSE AND local_work_item_type IN ('TASK','BUG','RISK','INCIDENT','CONTENT')))
);
CREATE INDEX {{.Prefix}}integration_sm_tenant ON {{.Prefix}}integration_state_mappings (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_import_jobs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 mapping_id BIGINT NOT NULL,
 state VARCHAR(24) NOT NULL,
 `scope` TEXT NOT NULL,
 dry_run_report_file_id BIGINT,
 counts TEXT NOT NULL,
 failures_file_id BIGINT,
 started_at DATETIME,
 finished_at DATETIME,
 job_id BIGINT,
 CONSTRAINT {{.Prefix}}integration_job_state_ck CHECK (state IN ('PLANNED','DRY_RUN_RUNNING','DRY_RUN_DONE','RUNNING','SUCCEEDED','PARTIAL','FAILED','CANCELLED'))
);
CREATE INDEX {{.Prefix}}integration_job_tenant ON {{.Prefix}}integration_import_jobs (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_inbound_events (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 connection_id BIGINT NOT NULL,
 source VARCHAR(16) NOT NULL,
 event_kind VARCHAR(64) NOT NULL,
 delivery_key VARCHAR(128) NOT NULL,
 signature_ok BOOLEAN NOT NULL,
 payload_hash VARCHAR(64) NOT NULL,
 payload_file_id BIGINT NOT NULL,
 state VARCHAR(16) NOT NULL,
 `error` VARCHAR(500),
 processed_at DATETIME,
 CONSTRAINT {{.Prefix}}integration_in_source_ck CHECK (source IN ('CODEUP','FLOW','PROJEX')),
 CONSTRAINT {{.Prefix}}integration_in_state_ck CHECK (state IN ('RECEIVED','PROCESSED','IGNORED','FAILED'))
);
CREATE INDEX {{.Prefix}}integration_in_tenant ON {{.Prefix}}integration_inbound_events (tenant_id, id);
CREATE TABLE {{.Prefix}}integration_sync_log (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 version INTEGER NOT NULL,
 created_at DATETIME NOT NULL,
 created_by BIGINT NOT NULL,
 updated_at DATETIME NOT NULL,
 updated_by BIGINT NOT NULL,
 mapping_id BIGINT NOT NULL,
 direction VARCHAR(3) NOT NULL,
 external_kind VARCHAR(32) NOT NULL,
 external_id VARCHAR(64) NOT NULL,
 local_id BIGINT NOT NULL,
 `field` VARCHAR(32),
 outcome VARCHAR(32) NOT NULL,
 detail TEXT NOT NULL,
 CONSTRAINT {{.Prefix}}integration_log_direction_ck CHECK (direction IN ('IN','OUT')),
 CONSTRAINT {{.Prefix}}integration_log_outcome_ck CHECK (outcome IN ('APPLIED','SKIPPED_NOT_MASTER','CONFLICT_MASTER_WINS','FAILED'))
);
CREATE INDEX {{.Prefix}}integration_log_tenant ON {{.Prefix}}integration_sync_log (tenant_id, id);
CREATE UNIQUE INDEX {{.Prefix}}integration_pm_project ON {{.Prefix}}integration_project_mappings (tenant_id, project_id);
CREATE UNIQUE INDEX {{.Prefix}}integration_pm_external ON {{.Prefix}}integration_project_mappings (connection_id, external_project_id);
CREATE UNIQUE INDEX {{.Prefix}}integration_ref_external ON {{.Prefix}}integration_external_refs (mapping_id, external_kind, external_id);
CREATE UNIQUE INDEX {{.Prefix}}integration_sm_external ON {{.Prefix}}integration_state_mappings (mapping_id, external_type, external_status_id);
CREATE UNIQUE INDEX {{.Prefix}}integration_in_delivery ON {{.Prefix}}integration_inbound_events (connection_id, source, delivery_key);
-- +goose Down
DROP TABLE {{.Prefix}}integration_sync_log;
DROP TABLE {{.Prefix}}integration_inbound_events;
DROP TABLE {{.Prefix}}integration_import_jobs;
DROP TABLE {{.Prefix}}integration_state_mappings;
DROP TABLE {{.Prefix}}integration_external_refs;
DROP TABLE {{.Prefix}}integration_project_mappings;
DROP TABLE {{.Prefix}}integration_connections;
