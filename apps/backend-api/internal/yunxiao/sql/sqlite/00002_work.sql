-- +goose Up
CREATE TABLE {{.Prefix}}delivery_worklogs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 principal_id BIGINT NOT NULL, actor_kind VARCHAR(8) NOT NULL, category VARCHAR(32) NOT NULL,
 work_date VARCHAR(10) NOT NULL, minutes BIGINT NOT NULL, cost_micros BIGINT NULL,
 execution_id BIGINT NULL, note VARCHAR(500) NOT NULL, state VARCHAR(16) NOT NULL,
 approved_by BIGINT NULL, approved_at DATETIME NULL, version INTEGER NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_worklog_execution UNIQUE(tenant_id, execution_id),
 CONSTRAINT {{.Prefix}}ck_worklog_minutes CHECK(minutes > 0 AND cost_micros >= 0)
);
CREATE INDEX {{.Prefix}}idx_worklog_project ON {{.Prefix}}delivery_worklogs(tenant_id,project_id,id);
CREATE INDEX {{.Prefix}}idx_worklog_day ON {{.Prefix}}delivery_worklogs(tenant_id,principal_id,work_date);
CREATE TABLE {{.Prefix}}delivery_workflow_configs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 work_item_type VARCHAR(16) NOT NULL, document TEXT NOT NULL, version INTEGER NOT NULL,
 updated_at DATETIME NOT NULL, updated_by BIGINT NOT NULL,
 CONSTRAINT {{.Prefix}}uq_workflow_type UNIQUE(tenant_id,project_id,work_item_type)
);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_workflow_configs;
DROP TABLE {{.Prefix}}delivery_worklogs;
