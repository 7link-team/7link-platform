-- Source: internal/modules/federation/sql/postgres/00001_identity_providers.sql:1-38.
-- Validation: internal/modules/federation/service.go:596-623;
-- internal/modules/federation/verifier.go:203-223.
-- The principal reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_identity_providers (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    provider_type VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'oidc',
    issuer VARCHAR(255) COLLATE "C" NOT NULL,
    client_id VARCHAR(128) COLLATE "C" NOT NULL,
    client_secret_ciphertext TEXT COLLATE "C" NOT NULL DEFAULT '',
    scopes VARCHAR(255) COLLATE "C" NOT NULL DEFAULT 'openid profile email',
    auto_provision BOOLEAN NOT NULL DEFAULT TRUE,
    default_role_id BIGINT NULL,
    status VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_type
        CHECK (provider_type IN ('oidc', 'saml')),
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_identity_providers_fields
        CHECK (octet_length(name) >= 1 AND octet_length(name) <= 128
            AND octet_length(issuer) >= 1 AND octet_length(issuer) <= 255
            AND octet_length(client_id) >= 1 AND octet_length(client_id) <= 128
            AND octet_length(scopes) >= 1 AND octet_length(scopes) <= 255),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_issuer UNIQUE (tenant_id, issuer),
    CONSTRAINT {{.Prefix}}uq_iam_identity_providers_tenant_id UNIQUE (tenant_id, id)
);
CREATE INDEX {{.Prefix}}idx_iam_identity_providers_tenant ON {{.Prefix}}iam_identity_providers (tenant_id, status, name);
CREATE TABLE {{.Prefix}}iam_identity_links (
    provider_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL,
    external_subject VARCHAR(128) COLLATE "C" NOT NULL,
    email VARCHAR(255) NOT NULL DEFAULT '',
    display_name VARCHAR(128) NOT NULL DEFAULT '',
    last_login_at TIMESTAMPTZ(3) NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    PRIMARY KEY (provider_id, tenant_id, external_subject),
    CONSTRAINT {{.Prefix}}chk_iam_identity_links_fields
        CHECK (octet_length(external_subject) >= 1 AND octet_length(external_subject) <= 128
            AND octet_length(email) <= 255 AND octet_length(display_name) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_identity_links_provider_principal UNIQUE (provider_id, principal_id),
    CONSTRAINT {{.Prefix}}fk_iam_identity_links_provider
        FOREIGN KEY (tenant_id, provider_id)
        REFERENCES {{.Prefix}}iam_identity_providers (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_identity_links_principal ON {{.Prefix}}iam_identity_links (tenant_id, principal_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_identity_links;
DROP TABLE {{.Prefix}}iam_identity_providers;
