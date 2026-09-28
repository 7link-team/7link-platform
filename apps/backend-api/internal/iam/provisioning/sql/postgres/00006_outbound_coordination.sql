-- Durable claims must not be expired: remote requests cannot be fenced by database leases.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN operation_token BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN operation_kind VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN operation_resource_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN operation_resource_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN operation_started_at TIMESTAMPTZ(3) NULL;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN retiring BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN sync_resource_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN sync_resource_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN sync_failures BIGINT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN sync_after_ms BIGINT NOT NULL DEFAULT 0;

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}scim_coordination_rollback_guard (n BIGINT CHECK (n = 0));
INSERT INTO {{.Prefix}}scim_coordination_rollback_guard (n) SELECT COUNT(*) FROM {{.Prefix}}iam_scim_targets WHERE operation_token <> 0 OR retiring = TRUE;
DROP TABLE {{.Prefix}}scim_coordination_rollback_guard;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN sync_after_ms;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN sync_failures;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN sync_resource_id;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN sync_resource_type;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN retiring;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN operation_started_at;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN operation_kind;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN operation_resource_id;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN operation_resource_type;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN operation_token;
