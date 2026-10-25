-- +goose Up
CREATE TABLE {{.Prefix}}delivery_plans (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 proposed_by BIGINT NOT NULL, source_kind VARCHAR(16) NOT NULL, content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL,
 coverage_ok BOOLEAN NOT NULL, round INT NOT NULL, state VARCHAR(16) NOT NULL, feedback VARCHAR(2000),
 decided_by BIGINT, decided_at TIMESTAMPTZ(3), created_at TIMESTAMPTZ(3) NOT NULL, version INT NOT NULL,
 CONSTRAINT {{.Prefix}}delivery_plan_round UNIQUE(tenant_id,work_item_id,round),
 CONSTRAINT {{.Prefix}}delivery_plan_round_limit CHECK(round BETWEEN 1 AND 3),
 CONSTRAINT {{.Prefix}}delivery_plan_state CHECK(state IN ('PROPOSED','APPROVED','REJECTED','SUPERSEDED')),
 CONSTRAINT {{.Prefix}}delivery_plan_source CHECK(source_kind='HUMAN'),
 CONSTRAINT {{.Prefix}}delivery_plan_version CHECK(version > 0)
);
CREATE INDEX {{.Prefix}}delivery_plan_list ON {{.Prefix}}delivery_plans(tenant_id,project_id,work_item_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_plans;
