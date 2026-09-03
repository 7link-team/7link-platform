-- Source: internal/modules/federation/sql/postgres/00002_saml_service_providers.sql:1-25.
-- Validation: internal/modules/federation/saml.go:941-956;
-- internal/modules/federation/saml.go:177-234.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_saml_service_providers (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    entity_id VARCHAR(255) COLLATE "C" NOT NULL,
    metadata_xml TEXT NOT NULL,
    status VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_status
        CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_saml_service_providers_fields
        CHECK (octet_length(name) >= 1 AND octet_length(name) <= 128
            AND octet_length(entity_id) >= 1 AND octet_length(entity_id) <= 255
            AND octet_length(metadata_xml) >= 1 AND octet_length(metadata_xml) <= 65536),
    CONSTRAINT {{.Prefix}}uq_iam_saml_sps_tenant_entity UNIQUE (tenant_id, entity_id)
);
CREATE INDEX {{.Prefix}}idx_iam_saml_service_providers_tenant ON {{.Prefix}}iam_saml_service_providers (tenant_id, status, name);

CREATE TABLE {{.Prefix}}iam_saml_idp_keys (
    key_id VARCHAR(128) COLLATE "C" PRIMARY KEY,
    cert_pem TEXT NOT NULL,
    key_ciphertext TEXT COLLATE "C" NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    rotated_at TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}chk_iam_saml_idp_keys_id
        CHECK (octet_length(key_id) >= 1 AND octet_length(key_id) <= 128)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_saml_idp_keys;
DROP TABLE {{.Prefix}}iam_saml_service_providers;
