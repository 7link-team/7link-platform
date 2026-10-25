-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_items ADD COLUMN out_of_scope_confirmed_at DATETIME(3) NULL;
CREATE TABLE {{.Prefix}}delivery_readiness_checks (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 work_item_version INT NOT NULL,
 trigger_kind VARCHAR(32) NOT NULL,
 ok BOOLEAN NOT NULL,
 issues JSON NOT NULL,
 checked_by VARCHAR(32) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_delivery_readiness_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (work_item_version>0),
 CHECK (trigger_kind IN ('EDIT','DISPATCH','REQUIREMENT_VERSIONED','MANUAL')),
 CHECK (checked_by IN ('SYSTEM','AGENT_JUDGE'))
);
CREATE INDEX {{.Prefix}}idx_delivery_readiness_work ON {{.Prefix}}delivery_readiness_checks (tenant_id,work_item_id,created_at,id);
CREATE TABLE {{.Prefix}}delivery_work_item_dependencies (
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 depends_on_id BIGINT NOT NULL,
 PRIMARY KEY (tenant_id,work_item_id,depends_on_id),
 CONSTRAINT {{.Prefix}}fk_delivery_dependency_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_delivery_dependency_before FOREIGN KEY (tenant_id,project_id,depends_on_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (work_item_id<>depends_on_id)
);
CREATE INDEX {{.Prefix}}idx_delivery_dependency_before ON {{.Prefix}}delivery_work_item_dependencies (tenant_id,depends_on_id);
CREATE TABLE {{.Prefix}}delivery_work_item_transitions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 from_state VARCHAR(32) NULL,
 to_state VARCHAR(32) NOT NULL,
 trigger_kind VARCHAR(32) NOT NULL,
 actor_kind VARCHAR(16) NOT NULL,
 actor_id BIGINT NULL,
 authority_source VARCHAR(128) NOT NULL,
 decision_id BIGINT NULL,
 reason_code VARCHAR(64) NOT NULL,
 source_ref VARCHAR(128) NULL,
 version_after INT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_transition_source UNIQUE (tenant_id,work_item_id,source_ref),
 CONSTRAINT {{.Prefix}}fk_delivery_transition_work FOREIGN KEY (tenant_id,project_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,project_id,id) ON DELETE RESTRICT,
 CHECK (version_after>0),
 CHECK (trigger_kind IN ('COMMAND','DECISION','SYSTEM')),
 CHECK (actor_kind IN ('HUMAN','AGENT','SYSTEM'))
);
CREATE INDEX {{.Prefix}}idx_delivery_transition_work ON {{.Prefix}}delivery_work_item_transitions (tenant_id,work_item_id,created_at,id);

-- +goose Down
-- Refuse rollback if it would erase preparation confirmation or immutable history.
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_readiness_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_readiness_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_readiness_checks;
INSERT INTO {{.Prefix}}delivery_readiness_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_item_transitions;
INSERT INTO {{.Prefix}}delivery_readiness_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_item_dependencies;
INSERT INTO {{.Prefix}}delivery_readiness_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_items WHERE out_of_scope_confirmed_at IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_readiness_rollback_guard;
DROP TABLE {{.Prefix}}delivery_work_item_transitions;
DROP TABLE {{.Prefix}}delivery_work_item_dependencies;
DROP TABLE {{.Prefix}}delivery_readiness_checks;
ALTER TABLE {{.Prefix}}delivery_work_items DROP COLUMN out_of_scope_confirmed_at;
