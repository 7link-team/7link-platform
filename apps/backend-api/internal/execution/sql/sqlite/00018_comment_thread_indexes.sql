-- +goose Up
-- Explicit supporting indexes keep foreign keys valid when sequence indexes roll back.
CREATE INDEX IF NOT EXISTS {{.Prefix}}idx_exec_comment_message_thread ON {{.Prefix}}execution_artifact_comment_messages(tenant_id,thread_id);
CREATE INDEX IF NOT EXISTS {{.Prefix}}idx_exec_comment_conversion_thread ON {{.Prefix}}execution_artifact_comment_conversions(tenant_id,thread_id);
-- +goose Down
-- Retain these supporting indexes until the comment tables are removed by migration 6.
-- Removing them here would make the published migration 14 rollback invalid on MySQL.
SELECT 1;
