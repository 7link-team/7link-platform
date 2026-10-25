-- +goose Up
CREATE TABLE {{.Prefix}}delivery_plans_rebuilt (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL, work_item_id INTEGER NOT NULL,
 proposed_by INTEGER NOT NULL, source_kind VARCHAR(16) NOT NULL, content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL,
 coverage_ok BOOLEAN NOT NULL, round INT NOT NULL, state VARCHAR(16) NOT NULL, feedback VARCHAR(2000),
 decided_by INTEGER, decided_at DATETIME, created_at DATETIME NOT NULL, version INT NOT NULL,
 CONSTRAINT {{.Prefix}}delivery_plan_round UNIQUE(tenant_id,work_item_id,round),
 CONSTRAINT {{.Prefix}}delivery_plan_round_limit CHECK(round BETWEEN 1 AND 3),
 CONSTRAINT {{.Prefix}}delivery_plan_state CHECK(state IN ('PROPOSED','APPROVED','REJECTED','SUPERSEDED')),
 CONSTRAINT {{.Prefix}}delivery_plan_source CHECK(source_kind IN ('HUMAN','AGENT')),
 CONSTRAINT {{.Prefix}}delivery_plan_version CHECK(version > 0)
);
INSERT INTO {{.Prefix}}delivery_plans_rebuilt SELECT * FROM {{.Prefix}}delivery_plans;
DROP TABLE {{.Prefix}}delivery_plans;
ALTER TABLE {{.Prefix}}delivery_plans_rebuilt RENAME TO {{.Prefix}}delivery_plans;
CREATE INDEX {{.Prefix}}delivery_plan_list ON {{.Prefix}}delivery_plans(tenant_id,project_id,work_item_id,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_agent_plan_rollback_guard(n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}delivery_agent_plan_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_plans WHERE source_kind='AGENT';
DROP TABLE {{.Prefix}}delivery_agent_plan_rollback_guard;
CREATE TABLE {{.Prefix}}delivery_plans_rebuilt (
 id INTEGER NOT NULL PRIMARY KEY, tenant_id INTEGER NOT NULL, project_id INTEGER NOT NULL, work_item_id INTEGER NOT NULL,
 proposed_by INTEGER NOT NULL, source_kind VARCHAR(16) NOT NULL, content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL,
 coverage_ok BOOLEAN NOT NULL, round INT NOT NULL, state VARCHAR(16) NOT NULL, feedback VARCHAR(2000),
 decided_by INTEGER, decided_at DATETIME, created_at DATETIME NOT NULL, version INT NOT NULL,
 CONSTRAINT {{.Prefix}}delivery_plan_round UNIQUE(tenant_id,work_item_id,round),
 CONSTRAINT {{.Prefix}}delivery_plan_round_limit CHECK(round BETWEEN 1 AND 3),
 CONSTRAINT {{.Prefix}}delivery_plan_state CHECK(state IN ('PROPOSED','APPROVED','REJECTED','SUPERSEDED')),
 CONSTRAINT {{.Prefix}}delivery_plan_source CHECK(source_kind='HUMAN'),
 CONSTRAINT {{.Prefix}}delivery_plan_version CHECK(version > 0)
);
INSERT INTO {{.Prefix}}delivery_plans_rebuilt SELECT * FROM {{.Prefix}}delivery_plans;
DROP TABLE {{.Prefix}}delivery_plans;
ALTER TABLE {{.Prefix}}delivery_plans_rebuilt RENAME TO {{.Prefix}}delivery_plans;
CREATE INDEX {{.Prefix}}delivery_plan_list ON {{.Prefix}}delivery_plans(tenant_id,project_id,work_item_id,id);
