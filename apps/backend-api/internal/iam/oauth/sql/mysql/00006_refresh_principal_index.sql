-- Supports principal-wide token revocation in authentication security workflows.
-- Swap the FK's single-column supporting index explicitly in both directions;
-- relying on InnoDB to discard an automatically generated index is not repeatable
-- after the first down migration recreates that index explicitly.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_refresh_tokens
    ADD INDEX {{.Prefix}}idx_iam_refresh_principal_active (principal_id, revoked_at),
    DROP INDEX {{.Prefix}}fk_oauth_refresh_principal;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_refresh_tokens
    ADD INDEX {{.Prefix}}fk_oauth_refresh_principal (principal_id),
    DROP INDEX {{.Prefix}}idx_iam_refresh_principal_active;
