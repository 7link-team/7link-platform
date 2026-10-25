-- +goose Up
ALTER TABLE {{.Prefix}}delivery_projects ADD COLUMN constitution_version_no INT NULL CHECK (constitution_version_no>0);
CREATE TABLE {{.Prefix}}delivery_project_constitutions (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 version_no INT NOT NULL,
 body TEXT NOT NULL,
 body_sha256 VARCHAR(64) NOT NULL,
 state VARCHAR(16) NOT NULL,
 change_summary VARCHAR(2000) NOT NULL,
 proposed_by_kind VARCHAR(16) NOT NULL,
 proposed_by BIGINT NOT NULL,
 reviewed_by BIGINT NULL,
 reviewed_at DATETIME NULL,
 review_comment VARCHAR(2000) NULL,
 active_key BIGINT NULL,
 created_at DATETIME NOT NULL,
 updated_at DATETIME NOT NULL,
 version INT NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_constitution_id UNIQUE (id),
 CONSTRAINT {{.Prefix}}uq_delivery_constitution_version UNIQUE (tenant_id,project_id,version_no),
 CONSTRAINT {{.Prefix}}uq_delivery_constitution_active UNIQUE (active_key),
 CONSTRAINT {{.Prefix}}fk_delivery_constitution_project FOREIGN KEY (tenant_id,project_id) REFERENCES {{.Prefix}}delivery_projects (tenant_id,project_id) ON DELETE RESTRICT,
 CHECK (version_no>0 AND version>0),
 CHECK (length(CAST(body AS BLOB)) BETWEEN 1 AND 65536),
 CHECK (length(body_sha256)=64),
 CHECK (length(change_summary) BETWEEN 1 AND 2000),
 CHECK (length(review_comment)<=2000),
 CHECK (state IN ('DRAFT','IN_REVIEW','ACTIVE','SUPERSEDED','REJECTED')),
 CHECK (proposed_by_kind IN ('HUMAN','AGENT')),
 CHECK ((state='ACTIVE' AND active_key IS NOT NULL AND active_key=project_id) OR (state<>'ACTIVE' AND active_key IS NULL)),
 CHECK (state NOT IN ('ACTIVE','SUPERSEDED','REJECTED') OR (reviewed_by IS NOT NULL AND reviewed_at IS NOT NULL)),
 CHECK (state<>'REJECTED' OR (review_comment IS NOT NULL AND length(review_comment)>0))
);
CREATE INDEX {{.Prefix}}idx_delivery_constitution_project ON {{.Prefix}}delivery_project_constitutions (tenant_id,project_id,state,version_no);

-- +goose Down
-- Refuse to erase proposed content, review decisions or the active pointer.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}delivery_constitution_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_constitution_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_project_constitutions;
INSERT INTO {{.Prefix}}delivery_constitution_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_projects WHERE constitution_version_no IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_constitution_rollback_guard;
DROP TABLE {{.Prefix}}delivery_project_constitutions;
ALTER TABLE {{.Prefix}}delivery_projects DROP COLUMN constitution_version_no;
