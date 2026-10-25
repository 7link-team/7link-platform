-- +goose Up
CREATE TABLE {{.Prefix}}delivery_project_stage_transitions (
 tenant_id INTEGER NOT NULL,
 id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 from_stage VARCHAR(32) NOT NULL,
 to_stage VARCHAR(32) NOT NULL,
 actor_kind VARCHAR(16) NOT NULL,
 actor_id INTEGER NOT NULL,
 authority_source VARCHAR(128) NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 blocked_kind VARCHAR(32) NULL,
 criteria_snapshot TEXT NOT NULL,
 scope_change_id INTEGER NULL,
 decision_id INTEGER NULL,
 version_after INT NOT NULL,
 occurred_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_stage_version UNIQUE (tenant_id,project_id,version_after),
 CONSTRAINT {{.Prefix}}fk_delivery_stage_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CHECK (version_after>1 AND from_stage<>to_stage),
 CHECK (actor_kind IN ('HUMAN','AGENT','SYSTEM'))
);
CREATE INDEX {{.Prefix}}idx_delivery_stage_transitions_project ON {{.Prefix}}delivery_project_stage_transitions (tenant_id,project_id,occurred_at,id);

-- +goose Down
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}delivery_stage_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_stage_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_project_stage_transitions;
DROP TABLE {{.Prefix}}delivery_stage_rollback_guard;
DROP TABLE {{.Prefix}}delivery_project_stage_transitions;
