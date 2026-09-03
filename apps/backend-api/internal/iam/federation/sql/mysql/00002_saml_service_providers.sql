-- Source: internal/modules/federation/sql/mysql/00002_saml_service_providers.sql:1-24.
-- Validation: internal/modules/federation/saml.go:941-956;
-- internal/modules/federation/saml.go:177-234.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_saml_service_providers (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    entity_id VARCHAR(255) COLLATE utf8mb4_bin NOT NULL,
    metadata_xml MEDIUMTEXT NOT NULL,
    status VARCHAR(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_fields
        CHECK (OCTET_LENGTH(name) >= 1 AND OCTET_LENGTH(name) <= 128
            AND OCTET_LENGTH(entity_id) >= 1 AND OCTET_LENGTH(entity_id) <= 255
            AND OCTET_LENGTH(metadata_xml) >= 1 AND OCTET_LENGTH(metadata_xml) <= 65536),
    CONSTRAINT {{.Prefix}}uq_iam_saml_sps_tenant_entity UNIQUE (tenant_id, entity_id),
    KEY {{.Prefix}}idx_iam_saml_service_providers_tenant (tenant_id, status, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE {{.Prefix}}iam_saml_idp_keys (
    key_id VARCHAR(128) COLLATE utf8mb4_bin PRIMARY KEY,
    cert_pem TEXT NOT NULL,
    key_ciphertext TEXT COLLATE utf8mb4_bin NOT NULL,
    created_at DATETIME(3) NOT NULL,
    rotated_at DATETIME(3) NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_idp_keys_id
        CHECK (OCTET_LENGTH(key_id) >= 1 AND OCTET_LENGTH(key_id) <= 128)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_saml_idp_keys;
DROP TABLE {{.Prefix}}iam_saml_service_providers;
