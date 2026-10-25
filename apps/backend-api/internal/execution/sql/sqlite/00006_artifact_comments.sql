-- +goose Up
CREATE TABLE {{.Prefix}}execution_artifact_comment_threads (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 execution_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL, evidence_id BIGINT NOT NULL,
 visibility VARCHAR(16) NOT NULL, state VARCHAR(16) NOT NULL, anchor_snapshot TEXT NOT NULL,
 first_body TEXT NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL, version INT NOT NULL,
 CONSTRAINT {{.Prefix}}uq_exec_comment_tenant UNIQUE(tenant_id,id),
 CONSTRAINT {{.Prefix}}ck_exec_comment_visibility CHECK(visibility IN ('INTERNAL','CLIENT')),
 CONSTRAINT {{.Prefix}}ck_exec_comment_state CHECK(state IN ('OPEN','RESOLVED')),
 CONSTRAINT {{.Prefix}}ck_exec_comment_version CHECK(version>0),
 CONSTRAINT {{.Prefix}}fk_exec_comment_execution FOREIGN KEY(tenant_id,execution_id) REFERENCES {{.Prefix}}execution_executions(tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_exec_comments_source ON {{.Prefix}}execution_artifact_comment_threads(tenant_id,execution_id,id);
CREATE TABLE {{.Prefix}}execution_artifact_comment_messages (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, thread_id BIGINT NOT NULL,
 body TEXT NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}fk_exec_comment_message FOREIGN KEY(tenant_id,thread_id) REFERENCES {{.Prefix}}execution_artifact_comment_threads(tenant_id,id) ON DELETE RESTRICT
);
CREATE TABLE {{.Prefix}}execution_artifact_comment_conversions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, thread_id BIGINT NOT NULL,
 target_kind VARCHAR(16) NOT NULL, target_id BIGINT NOT NULL, actor_id BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_exec_comment_conversion UNIQUE(tenant_id,thread_id,target_kind),
 CONSTRAINT {{.Prefix}}ck_exec_comment_target CHECK(target_kind IN ('FEEDBACK','DEFECT')),
 CONSTRAINT {{.Prefix}}fk_exec_comment_conversion FOREIGN KEY(tenant_id,thread_id) REFERENCES {{.Prefix}}execution_artifact_comment_threads(tenant_id,id) ON DELETE RESTRICT
);
-- +goose Down
DROP TABLE {{.Prefix}}execution_artifact_comment_conversions;
DROP TABLE {{.Prefix}}execution_artifact_comment_messages;
DROP TABLE {{.Prefix}}execution_artifact_comment_threads;
