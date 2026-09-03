-- Source: internal/modules/federation/sql/sqlite/00002_saml_service_providers.sql:1-25.
-- Validation: internal/modules/federation/saml.go:941-956;
-- internal/modules/federation/saml.go:177-234.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_saml_service_providers (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    entity_id TEXT COLLATE BINARY NOT NULL,
    metadata_xml TEXT NOT NULL,
    status TEXT COLLATE BINARY NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_fields
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128
            AND length(CAST(entity_id AS BLOB)) >= 1 AND length(CAST(entity_id AS BLOB)) <= 255
            AND length(CAST(metadata_xml AS BLOB)) >= 1 AND length(CAST(metadata_xml AS BLOB)) <= 65536),
    CONSTRAINT {{.Prefix}}uq_iam_saml_sps_tenant_entity UNIQUE (tenant_id, entity_id)
);
CREATE INDEX {{.Prefix}}idx_iam_saml_service_providers_tenant ON {{.Prefix}}iam_saml_service_providers (tenant_id, status, name);

CREATE TABLE {{.Prefix}}iam_saml_idp_keys (
    key_id TEXT COLLATE BINARY PRIMARY KEY,
    cert_pem TEXT NOT NULL,
    key_ciphertext TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    rotated_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_idp_keys_id
        CHECK (length(CAST(key_id AS BLOB)) >= 1 AND length(CAST(key_id AS BLOB)) <= 128)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_saml_idp_keys;
DROP TABLE {{.Prefix}}iam_saml_service_providers;
