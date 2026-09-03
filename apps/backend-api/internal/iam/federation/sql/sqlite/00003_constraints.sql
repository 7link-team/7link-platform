-- The cross-module principal reference preserves the source ON DELETE CASCADE behavior.
-- Source: internal/modules/federation/sql/sqlite/00001_identity_providers.sql:35.
-- SQLite cannot add or drop foreign keys in place, so the link table is rebuilt.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_identity_links
    RENAME TO {{.Prefix}}iam_identity_links_old;
CREATE TABLE {{.Prefix}}iam_identity_links (
    provider_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    external_subject TEXT COLLATE BINARY NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    last_login_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (provider_id, tenant_id, external_subject),
    CONSTRAINT {{.Prefix}}chk_iam_identity_links_fields
        CHECK (length(CAST(external_subject AS BLOB)) >= 1 AND length(CAST(external_subject AS BLOB)) <= 128
            AND length(CAST(email AS BLOB)) <= 255 AND length(CAST(display_name AS BLOB)) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_identity_links_provider_principal UNIQUE (provider_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_identity_links_provider
        FOREIGN KEY (tenant_id, provider_id)
        REFERENCES {{.Prefix}}iam_identity_providers (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_iam_identity_links_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_identity_links
SELECT * FROM {{.Prefix}}iam_identity_links_old;
DROP TABLE {{.Prefix}}iam_identity_links_old;
CREATE INDEX {{.Prefix}}idx_iam_identity_links_principal
    ON {{.Prefix}}iam_identity_links (tenant_id, principal_id);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_identity_links
    RENAME TO {{.Prefix}}iam_identity_links_old;
CREATE TABLE {{.Prefix}}iam_identity_links (
    provider_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    external_subject TEXT COLLATE BINARY NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    last_login_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (provider_id, tenant_id, external_subject),
    CONSTRAINT {{.Prefix}}chk_iam_identity_links_fields
        CHECK (length(CAST(external_subject AS BLOB)) >= 1 AND length(CAST(external_subject AS BLOB)) <= 128
            AND length(CAST(email AS BLOB)) <= 255 AND length(CAST(display_name AS BLOB)) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_identity_links_provider_principal UNIQUE (provider_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_identity_links_provider
        FOREIGN KEY (tenant_id, provider_id)
        REFERENCES {{.Prefix}}iam_identity_providers (tenant_id, id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_identity_links
SELECT * FROM {{.Prefix}}iam_identity_links_old;
DROP TABLE {{.Prefix}}iam_identity_links_old;
CREATE INDEX {{.Prefix}}idx_iam_identity_links_principal
    ON {{.Prefix}}iam_identity_links (tenant_id, principal_id);
