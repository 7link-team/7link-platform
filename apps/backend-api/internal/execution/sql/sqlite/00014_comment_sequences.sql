-- +goose Up
-- Before this migration the only message writer creates one first message per thread.
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN seq BIGINT NOT NULL DEFAULT 1 CHECK (seq > 0);
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions ADD COLUMN seq BIGINT NOT NULL DEFAULT 2 CHECK (seq > 0);
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN actor_kind VARCHAR(16) NOT NULL DEFAULT 'PRINCIPAL';
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions ADD COLUMN actor_kind VARCHAR(16) NOT NULL DEFAULT 'PRINCIPAL';
UPDATE {{.Prefix}}execution_artifact_comment_conversions SET seq = 2 + (SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_conversions older WHERE older.tenant_id = {{.Prefix}}execution_artifact_comment_conversions.tenant_id AND older.thread_id = {{.Prefix}}execution_artifact_comment_conversions.thread_id AND (older.created_at < {{.Prefix}}execution_artifact_comment_conversions.created_at OR (older.created_at = {{.Prefix}}execution_artifact_comment_conversions.created_at AND older.id < {{.Prefix}}execution_artifact_comment_conversions.id)));
CREATE UNIQUE INDEX {{.Prefix}}exec_comment_message_seq ON {{.Prefix}}execution_artifact_comment_messages(tenant_id,thread_id,seq);
CREATE UNIQUE INDEX {{.Prefix}}exec_comment_conversion_seq ON {{.Prefix}}execution_artifact_comment_conversions(tenant_id,thread_id,seq);
-- +goose Down
-- Reply sequences cannot be reconstructed by the legacy first-message writer.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}exec_comment_seq_rollback_guard (n INT NOT NULL CHECK (n = 0));
INSERT INTO {{.Prefix}}exec_comment_seq_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_messages WHERE seq <> 1 OR actor_kind <> 'PRINCIPAL';
INSERT INTO {{.Prefix}}exec_comment_seq_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_conversions WHERE actor_kind <> 'PRINCIPAL';
DROP TABLE {{.Prefix}}exec_comment_seq_rollback_guard;
DROP INDEX {{.Prefix}}exec_comment_message_seq;
DROP INDEX {{.Prefix}}exec_comment_conversion_seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions DROP COLUMN seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN actor_kind;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions DROP COLUMN actor_kind;
