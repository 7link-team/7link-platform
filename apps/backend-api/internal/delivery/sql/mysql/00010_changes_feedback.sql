-- +goose Up
CREATE TABLE {{.Prefix}}delivery_change_records (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 requirement_id BIGINT NOT NULL, kind VARCHAR(16) NOT NULL,
 from_version_no INT NOT NULL, to_version_no INT NOT NULL,
 summary VARCHAR(2000) NOT NULL, diff TEXT NOT NULL,
 authority_source VARCHAR(128) NOT NULL, actor_id BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}uq_delivery_change_version UNIQUE (tenant_id,requirement_id,to_version_no),
 CONSTRAINT {{.Prefix}}fk_delivery_change_requirement FOREIGN KEY (tenant_id,requirement_id) REFERENCES {{.Prefix}}delivery_requirements (tenant_id,id) ON DELETE RESTRICT,
 CHECK (kind='SCOPE' AND from_version_no>0 AND to_version_no=from_version_no+1)
);
CREATE TABLE {{.Prefix}}delivery_change_impacts (
 tenant_id BIGINT NOT NULL, change_record_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL, impact VARCHAR(16) NOT NULL, version_before INT NOT NULL,
 PRIMARY KEY (tenant_id,change_record_id,work_item_id),
 CONSTRAINT {{.Prefix}}fk_delivery_impact_change FOREIGN KEY (tenant_id,change_record_id) REFERENCES {{.Prefix}}delivery_change_records (tenant_id,id) ON DELETE RESTRICT,
 CONSTRAINT {{.Prefix}}fk_delivery_impact_work FOREIGN KEY (tenant_id,work_item_id) REFERENCES {{.Prefix}}delivery_work_items (tenant_id,id) ON DELETE RESTRICT,
 CHECK (impact='STALE' AND version_before>0)
);
CREATE TABLE {{.Prefix}}delivery_result_feedback (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL,
 result_version_id BIGINT NOT NULL, kind VARCHAR(16) NOT NULL,
 category VARCHAR(32) NOT NULL, detail VARCHAR(2000) NOT NULL,
 actor_id BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (tenant_id,id),
 CONSTRAINT {{.Prefix}}fk_delivery_feedback_result FOREIGN KEY (tenant_id,result_version_id) REFERENCES {{.Prefix}}delivery_result_versions (tenant_id,id) ON DELETE RESTRICT,
 CHECK ((kind='COMMENT' AND category='') OR (kind='FEEDBACK' AND category IN ('QUALITY_REJECTION','STRATEGY_CHANGE','SCOPE_CHANGE')))
);
CREATE INDEX {{.Prefix}}idx_delivery_result_feedback ON {{.Prefix}}delivery_result_feedback (tenant_id,project_id,result_version_id,created_at,id);

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_change_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_change_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_change_records;
INSERT INTO {{.Prefix}}delivery_change_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_feedback;
DROP TABLE {{.Prefix}}delivery_change_rollback_guard;
DROP TABLE {{.Prefix}}delivery_result_feedback;
DROP TABLE {{.Prefix}}delivery_change_impacts;
DROP TABLE {{.Prefix}}delivery_change_records;
