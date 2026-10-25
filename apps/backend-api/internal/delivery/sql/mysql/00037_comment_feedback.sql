-- +goose Up
ALTER TABLE {{.Prefix}}delivery_result_feedback ADD COLUMN comment_thread_id BIGINT NULL;
ALTER TABLE {{.Prefix}}delivery_result_feedback ADD COLUMN anchor_snapshot TEXT NULL;
ALTER TABLE {{.Prefix}}delivery_result_feedback ADD COLUMN location VARCHAR(2000) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}delivery_result_feedback ADD COLUMN expected VARCHAR(2000) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX {{.Prefix}}uq_result_comment_feedback ON {{.Prefix}}delivery_result_feedback(tenant_id,comment_thread_id);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}result_comment_feedback_guard(n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}result_comment_feedback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_result_feedback WHERE comment_thread_id IS NOT NULL OR anchor_snapshot IS NOT NULL OR location<>'' OR expected<>'';
DROP TABLE {{.Prefix}}result_comment_feedback_guard;
DROP INDEX {{.Prefix}}uq_result_comment_feedback ON {{.Prefix}}delivery_result_feedback;
ALTER TABLE {{.Prefix}}delivery_result_feedback DROP COLUMN expected;
ALTER TABLE {{.Prefix}}delivery_result_feedback DROP COLUMN location;
ALTER TABLE {{.Prefix}}delivery_result_feedback DROP COLUMN anchor_snapshot;
ALTER TABLE {{.Prefix}}delivery_result_feedback DROP COLUMN comment_thread_id;
