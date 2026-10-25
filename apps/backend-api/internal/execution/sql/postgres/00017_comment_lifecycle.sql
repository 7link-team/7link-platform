-- +goose Up
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads ADD COLUMN anchor_checked_version BIGINT NULL;
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads ADD COLUMN relocated_anchor TEXT NULL;
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads ADD COLUMN anchor_status VARCHAR(16) NOT NULL DEFAULT 'CURRENT';
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN edit_count INT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN edited_at TIMESTAMPTZ(3) NULL;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN retracted_at TIMESTAMPTZ(3) NULL;
CREATE TABLE {{.Prefix}}execution_artifact_comment_events (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, thread_id BIGINT NOT NULL,
 seq BIGINT NOT NULL, kind VARCHAR(32) NOT NULL, actor_id BIGINT NOT NULL,
 actor_kind VARCHAR(16) NOT NULL, created_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_exec_comment_event_seq UNIQUE(tenant_id,thread_id,seq),
 CONSTRAINT {{.Prefix}}ck_exec_comment_event_seq CHECK(seq > 0),
 CONSTRAINT {{.Prefix}}fk_exec_comment_event_thread FOREIGN KEY(tenant_id,thread_id) REFERENCES {{.Prefix}}execution_artifact_comment_threads(tenant_id,id) ON DELETE RESTRICT
);
-- +goose Down
CREATE TEMPORARY TABLE {{.Prefix}}exec_comment_lifecycle_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}exec_comment_lifecycle_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_events;
INSERT INTO {{.Prefix}}exec_comment_lifecycle_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_messages WHERE edit_count<>0 OR retracted_at IS NOT NULL;
INSERT INTO {{.Prefix}}exec_comment_lifecycle_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_threads WHERE anchor_checked_version IS NOT NULL;
DROP TABLE {{.Prefix}}exec_comment_lifecycle_guard;
DROP TABLE {{.Prefix}}execution_artifact_comment_events;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN retracted_at;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN edited_at;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN edit_count;
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads DROP COLUMN anchor_status;
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads DROP COLUMN relocated_anchor;
ALTER TABLE {{.Prefix}}execution_artifact_comment_threads DROP COLUMN anchor_checked_version;
