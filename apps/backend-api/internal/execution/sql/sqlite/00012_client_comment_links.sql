-- +goose Up
CREATE TABLE {{.Prefix}}execution_client_comment_links (
 thread_id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NOT NULL,
 delivery_id BIGINT NOT NULL, item_id VARCHAR(64) NOT NULL, contact_id BIGINT NOT NULL,
 first_body TEXT NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 anchor_snapshot TEXT NOT NULL, target_kind VARCHAR(32) NOT NULL DEFAULT '', target_id BIGINT, converted_at DATETIME, converted_by BIGINT,
 CONSTRAINT {{.Prefix}}ck_exec_client_conversion CHECK((target_kind='' AND target_id IS NULL AND converted_at IS NULL AND converted_by IS NULL) OR (target_kind='REJECTION_ITEM' AND target_id IS NOT NULL AND target_id>0 AND converted_at IS NOT NULL AND converted_by IS NOT NULL AND converted_by>0))
);
CREATE INDEX {{.Prefix}}idx_exec_client_delivery ON {{.Prefix}}execution_client_comment_links(tenant_id,project_id,delivery_id,item_id,thread_id);
-- +goose Down
-- Immutable client comments and rejection references must not be erased.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}execution_client_comment_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}execution_client_comment_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}execution_client_comment_links;
DROP TABLE {{.Prefix}}execution_client_comment_rollback_guard;
DROP TABLE {{.Prefix}}execution_client_comment_links;
