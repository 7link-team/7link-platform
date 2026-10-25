-- +goose Up
CREATE TABLE {{.Prefix}}delivery_project_mode_changes (
 tenant_id INTEGER NOT NULL,
 id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 from_mode VARCHAR(16) NOT NULL,
 to_mode VARCHAR(16) NOT NULL,
 trigger_kind VARCHAR(16) NOT NULL,
 reason VARCHAR(2000) NOT NULL,
 preconditions_snapshot TEXT NOT NULL,
 revoked_delegation_ids TEXT NOT NULL,
 revoked_spawn_grant_ids TEXT NOT NULL,
 actor_id INTEGER NOT NULL,
 authority_source VARCHAR(128) NOT NULL,
 version_after INT NOT NULL,
 occurred_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_mode_version UNIQUE (tenant_id,project_id,version_after),
 CONSTRAINT {{.Prefix}}fk_delivery_mode_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CHECK (from_mode IN ('HUMAN_ONLY','HYBRID','AUTONOMOUS')),
 CHECK (to_mode IN ('HUMAN_ONLY','HYBRID','AUTONOMOUS')),
 CHECK (trigger_kind IN ('MANUAL','TAKEOVER')),
 CHECK (id>0 AND tenant_id>0 AND project_id>0 AND actor_id>0),
 CHECK (version_after>1)
);
CREATE INDEX {{.Prefix}}idx_delivery_mode_project ON {{.Prefix}}delivery_project_mode_changes (tenant_id,project_id,occurred_at,id);

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_mode_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_mode_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_project_mode_changes;
DROP TABLE {{.Prefix}}delivery_mode_rollback_guard;
DROP TABLE {{.Prefix}}delivery_project_mode_changes;
