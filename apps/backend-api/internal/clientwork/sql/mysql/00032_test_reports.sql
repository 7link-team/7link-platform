-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_test_reports (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, project_id BIGINT NOT NULL, actor_id BIGINT NOT NULL,
 title VARCHAR(200) NOT NULL, snapshot LONGTEXT NOT NULL, state VARCHAR(16) NOT NULL, job_id BIGINT NOT NULL,
 binding_id BIGINT, file_id BIGINT, version_id BIGINT, content_hash VARCHAR(64),
 created_at DATETIME(3) NOT NULL, built_at DATETIME(3),
 PRIMARY KEY (tenant_id,id),
 CHECK (tenant_id>0 AND id>0 AND project_id>0 AND actor_id>0 AND job_id>0),
 CHECK ((state IN ('PENDING','FAILED') AND binding_id IS NULL AND file_id IS NULL AND version_id IS NULL AND content_hash IS NULL AND built_at IS NULL) OR
        (state='BUILT' AND binding_id IS NOT NULL AND file_id IS NOT NULL AND version_id IS NOT NULL AND content_hash IS NOT NULL AND binding_id>0 AND file_id>0 AND version_id>0 AND LENGTH(content_hash)=64 AND built_at IS NOT NULL))
);
CREATE INDEX {{.Prefix}}idx_clientwork_test_reports ON {{.Prefix}}clientwork_test_reports(tenant_id,project_id,id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}test_report_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}test_report_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}clientwork_test_reports;
DROP TABLE {{.Prefix}}test_report_rollback_guard;
DROP TABLE {{.Prefix}}clientwork_test_reports;
