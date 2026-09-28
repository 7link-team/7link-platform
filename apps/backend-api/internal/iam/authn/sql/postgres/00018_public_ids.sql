-- Sessions and passkeys keep their secret-keyed hash as primary key (the hash
-- primary key exemption in .claude/decisions.md) and publish a platform
-- snowflake as their API identity. The column is NULL only until the module's
-- start-up backfill assigns one to rows created before this migration; every
-- new row is inserted with it.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_sessions ADD COLUMN public_id BIGINT NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_sessions_public_id ON {{.Prefix}}iam_sessions (public_id);
ALTER TABLE {{.Prefix}}iam_passkeys ADD COLUMN public_id BIGINT NULL;
CREATE UNIQUE INDEX {{.Prefix}}uq_iam_passkeys_public_id ON {{.Prefix}}iam_passkeys (public_id);
-- +goose Down
DROP INDEX {{.Prefix}}uq_iam_passkeys_public_id;
ALTER TABLE {{.Prefix}}iam_passkeys DROP COLUMN public_id;
DROP INDEX {{.Prefix}}uq_iam_sessions_public_id;
ALTER TABLE {{.Prefix}}iam_sessions DROP COLUMN public_id;
