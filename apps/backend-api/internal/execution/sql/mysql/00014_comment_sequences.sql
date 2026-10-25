-- +goose Up
-- Before this migration the only message writer creates one first message per thread.
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN seq BIGINT NOT NULL DEFAULT 1;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions ADD COLUMN seq BIGINT NOT NULL DEFAULT 2;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD COLUMN actor_kind VARCHAR(16) NOT NULL DEFAULT 'PRINCIPAL';
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions ADD COLUMN actor_kind VARCHAR(16) NOT NULL DEFAULT 'PRINCIPAL';
UPDATE {{.Prefix}}execution_artifact_comment_conversions c JOIN (SELECT id, ROW_NUMBER() OVER (PARTITION BY tenant_id,thread_id ORDER BY created_at,id) + 1 AS backfill_seq FROM {{.Prefix}}execution_artifact_comment_conversions) ranked ON ranked.id = c.id SET c.seq = ranked.backfill_seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages ADD CONSTRAINT {{.Prefix}}exec_comment_message_seq_positive CHECK (seq > 0);
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions ADD CONSTRAINT {{.Prefix}}exec_comment_conversion_seq_positive CHECK (seq > 0);
CREATE UNIQUE INDEX {{.Prefix}}exec_comment_message_seq ON {{.Prefix}}execution_artifact_comment_messages(tenant_id,thread_id,seq);
CREATE UNIQUE INDEX {{.Prefix}}exec_comment_conversion_seq ON {{.Prefix}}execution_artifact_comment_conversions(tenant_id,thread_id,seq);
-- +goose Down
-- Reply sequences cannot be reconstructed by the legacy first-message writer.
CREATE TABLE IF NOT EXISTS {{.Prefix}}exec_comment_seq_rollback_guard (n INT NOT NULL CHECK (n = 0));
INSERT INTO {{.Prefix}}exec_comment_seq_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_messages WHERE seq <> 1 OR actor_kind <> 'PRINCIPAL';
INSERT INTO {{.Prefix}}exec_comment_seq_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_artifact_comment_conversions WHERE actor_kind <> 'PRINCIPAL';
DROP TABLE {{.Prefix}}exec_comment_seq_rollback_guard;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP CHECK {{.Prefix}}exec_comment_message_seq_positive;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions DROP CHECK {{.Prefix}}exec_comment_conversion_seq_positive;
DROP INDEX {{.Prefix}}exec_comment_message_seq ON {{.Prefix}}execution_artifact_comment_messages;
DROP INDEX {{.Prefix}}exec_comment_conversion_seq ON {{.Prefix}}execution_artifact_comment_conversions;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions DROP COLUMN seq;
ALTER TABLE {{.Prefix}}execution_artifact_comment_messages DROP COLUMN actor_kind;
ALTER TABLE {{.Prefix}}execution_artifact_comment_conversions DROP COLUMN actor_kind;
