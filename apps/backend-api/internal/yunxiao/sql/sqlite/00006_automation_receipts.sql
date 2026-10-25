-- +goose Up
CREATE TABLE {{.Prefix}}delivery_automation_rules (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 created_by BIGINT NOT NULL, name VARCHAR(80) NOT NULL, version INT NOT NULL,
 enabled BOOLEAN NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}ck_automation_rule_version CHECK(version>0)
);
CREATE TABLE {{.Prefix}}delivery_automation_runs (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 rule_id BIGINT NOT NULL, rule_version INT NOT NULL, rule_name VARCHAR(80) NOT NULL,
 created_by BIGINT NOT NULL, trigger_event_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 outcome VARCHAR(16) NOT NULL, error VARCHAR(500) NOT NULL, duration_ms BIGINT NOT NULL,
 created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_automation_run_trigger UNIQUE(tenant_id,rule_id,trigger_event_id,work_item_id),
 CONSTRAINT {{.Prefix}}ck_automation_run_outcome CHECK(outcome IN ('SUCCEEDED','FAILED','SKIPPED')),
 CONSTRAINT {{.Prefix}}ck_automation_run_duration CHECK(duration_ms>=0)
);
CREATE INDEX {{.Prefix}}idx_automation_run_rule ON {{.Prefix}}delivery_automation_runs(tenant_id,rule_id,created_at);
CREATE INDEX {{.Prefix}}idx_automation_run_work ON {{.Prefix}}delivery_automation_runs(tenant_id,work_item_id,created_at);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_automation_runs;
DROP TABLE {{.Prefix}}delivery_automation_rules;
