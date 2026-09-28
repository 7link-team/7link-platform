-- Source: internal/modules/federation/sql/sqlite/00001_identity_providers.sql:1-39.
-- Validation: internal/modules/federation/service.go:596-623;
-- internal/modules/federation/verifier.go:203-223.
-- The principal reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_identity_providers (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    provider_type TEXT COLLATE BINARY NOT NULL DEFAULT 'oidc',
    issuer TEXT COLLATE BINARY NOT NULL,
    client_id TEXT COLLATE BINARY NOT NULL,
    client_secret_ciphertext TEXT COLLATE BINARY NOT NULL DEFAULT '',
    scopes TEXT COLLATE BINARY NOT NULL DEFAULT 'openid profile email',
    auto_provision INTEGER NOT NULL DEFAULT 1 CHECK (auto_provision IN (0, 1)),
    default_role_id BIGINT NULL,
    status TEXT COLLATE BINARY NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml')),
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_fields
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128
            AND length(CAST(issuer AS BLOB)) >= 1 AND length(CAST(issuer AS BLOB)) <= 255
            AND length(CAST(client_id AS BLOB)) >= 1 AND length(CAST(client_id AS BLOB)) <= 128
            AND length(CAST(scopes AS BLOB)) >= 1 AND length(CAST(scopes AS BLOB)) <= 255),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_issuer UNIQUE (tenant_id, issuer),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_id UNIQUE (tenant_id, id)
);
CREATE INDEX {{.Prefix}}idx_iam_identity_providers_tenant ON {{.Prefix}}iam_identity_providers (tenant_id, status, name);

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
CREATE INDEX {{.Prefix}}idx_iam_identity_links_principal ON {{.Prefix}}iam_identity_links (tenant_id, principal_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_identity_links;
DROP TABLE {{.Prefix}}iam_identity_providers;
