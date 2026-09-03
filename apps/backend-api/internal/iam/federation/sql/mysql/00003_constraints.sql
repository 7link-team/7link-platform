-- The cross-module principal reference preserves the source ON DELETE CASCADE behavior.
-- Source: internal/modules/federation/sql/mysql/00001_identity_providers.sql:36.
-- MySQL DDL auto-commits; the single statement is independently recoverable.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links
    ADD CONSTRAINT {{.Prefix}}fk_iam_identity_links_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_identity_links
    DROP FOREIGN KEY {{.Prefix}}fk_iam_identity_links_principal;
