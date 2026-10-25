-- +goose Up
-- Explicit supporting indexes keep foreign keys valid when sequence indexes roll back.
SET @comment_thread_index_ddl = (SELECT IF(COUNT(*) = 0, 'CREATE INDEX {{.Prefix}}idx_exec_comment_message_thread ON {{.Prefix}}execution_artifact_comment_messages(tenant_id,thread_id)', 'SELECT 1') FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = '{{.Prefix}}execution_artifact_comment_messages' AND index_name = '{{.Prefix}}idx_exec_comment_message_thread');
PREPARE comment_thread_index_statement FROM @comment_thread_index_ddl;
EXECUTE comment_thread_index_statement;
DEALLOCATE PREPARE comment_thread_index_statement;
SET @comment_thread_index_ddl = (SELECT IF(COUNT(*) = 0, 'CREATE INDEX {{.Prefix}}idx_exec_comment_conversion_thread ON {{.Prefix}}execution_artifact_comment_conversions(tenant_id,thread_id)', 'SELECT 1') FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = '{{.Prefix}}execution_artifact_comment_conversions' AND index_name = '{{.Prefix}}idx_exec_comment_conversion_thread');
PREPARE comment_thread_index_statement FROM @comment_thread_index_ddl;
EXECUTE comment_thread_index_statement;
DEALLOCATE PREPARE comment_thread_index_statement;
-- +goose Down
-- Retain these supporting indexes until the comment tables are removed by migration 6.
-- Removing them here would make the published migration 14 rollback invalid on MySQL.
SELECT 1;
