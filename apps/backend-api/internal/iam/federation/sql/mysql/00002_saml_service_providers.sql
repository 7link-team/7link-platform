-- Source: internal/modules/federation/sql/mysql/00002_saml_service_providers.sql:1-24
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
    UNIQUE KEY {{.Prefix}}uq_iam_saml_service_providers_tenant_entity (tenant_id, entity_id),
    KEY {{.Prefix}}idx_iam_saml_service_providers_tenant (tenant_id, status, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
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