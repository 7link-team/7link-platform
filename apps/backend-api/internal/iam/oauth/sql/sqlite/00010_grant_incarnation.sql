-- +goose Up
-- Empty legacy grants deliberately require a new authorization after upgrade.
ALTER TABLE {{.Prefix}}iam_oauth_consents ADD COLUMN grant_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_oauth_codes ADD COLUMN grant_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}iam_refresh_tokens ADD COLUMN grant_id VARCHAR(64) NOT NULL DEFAULT '';

-- +goose Down
-- Schema rollback is NOT token revocation. Before running an old writer,
-- stop OAuth writes, invalidate old signing trust or wait for hard token expiry,
-- and retire pending consent requests. Never mix old and new authentication.
ALTER TABLE {{.Prefix}}iam_refresh_tokens DROP COLUMN grant_id;
ALTER TABLE {{.Prefix}}iam_oauth_codes DROP COLUMN grant_id;
ALTER TABLE {{.Prefix}}iam_oauth_consents DROP COLUMN grant_id;
