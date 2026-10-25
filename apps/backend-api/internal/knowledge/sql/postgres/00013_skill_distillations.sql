-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_skill_distillations (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 agent_principal_id BIGINT NOT NULL, source_execution_id BIGINT NOT NULL, source_result_id BIGINT NOT NULL,
 input_hash CHAR(64) NOT NULL, execution_id BIGINT NULL, skill_version_id BIGINT NULL,
 state VARCHAR(32) NOT NULL, created_by BIGINT NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_knowledge_distillation_state CHECK (state IN ('REQUESTED','RUNNING','DRAFT','FAILED')),
 CONSTRAINT {{.Prefix}}uq_knowledge_distillation_execution UNIQUE (tenant_id,execution_id)
);
CREATE INDEX {{.Prefix}}idx_knowledge_distillation_work ON {{.Prefix}}knowledge_skill_distillations(tenant_id,work_item_id,created_at);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_skill_distillations;
