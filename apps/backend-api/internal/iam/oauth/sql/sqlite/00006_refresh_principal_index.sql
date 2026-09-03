-- Supports principal-wide token revocation in authentication security workflows.
-- +goose Up
CREATE INDEX {{.Prefix}}idx_iam_refresh_principal_active
    ON {{.Prefix}}iam_refresh_tokens (principal_id, revoked_at);

-- +goose Down
DROP INDEX {{.Prefix}}idx_iam_refresh_principal_active;
