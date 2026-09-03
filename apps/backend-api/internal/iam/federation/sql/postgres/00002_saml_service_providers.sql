-- Source: internal/modules/federation/sql/postgres/00002_saml_service_providers.sql:1-25
-- +goose Up
CREATE TABLE {{.Prefix}}iam_saml_service_providers (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    entity_id VARCHAR(255) NOT NULL,
    metadata_xml TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE (tenant_id, entity_id)
);
CREATE INDEX {{.Prefix}}idx_iam_saml_service_providers_tenant ON {{.Prefix}}iam_saml_service_providers (tenant_id, status, name);

CREATE TABLE {{.Prefix}}iam_saml_idp_keys (
    key_id VARCHAR(128) PRIMARY KEY,
    cert_pem TEXT NOT NULL,
    key_ciphertext TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    rotated_at BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_saml_idp_keys;
DROP TABLE {{.Prefix}}iam_saml_service_providers;