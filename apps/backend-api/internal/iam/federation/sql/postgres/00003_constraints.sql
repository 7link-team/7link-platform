-- The cross-module principal reference preserves the source ON DELETE CASCADE behavior.
-- Source: internal/modules/federation/sql/postgres/00001_identity_providers.sql:34.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links
    ADD CONSTRAINT {{.Prefix}}fk_iam_identity_links_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_identity_links
    DROP CONSTRAINT {{.Prefix}}fk_iam_identity_links_principal;
