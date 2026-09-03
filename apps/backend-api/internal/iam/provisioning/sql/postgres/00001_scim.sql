-- Source: internal/modules/provisioning/sql/postgres/00001_scim.sql:1-45.
-- Validation: internal/modules/provisioning/service.go:96-201;
-- internal/modules/provisioning/scim.go:217-270.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_directories (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) COLLATE "C" NOT NULL,
    status VARCHAR(16) COLLATE "C" NOT NULL CHECK (status IN ('active', 'disabled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_name
        CHECK (octet_length(name) >= 1 AND octet_length(name) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_scim_directories_name UNIQUE (tenant_id, name_key)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_directories_tenant ON {{.Prefix}}iam_scim_directories (tenant_id, status, name_key, id);

CREATE TABLE {{.Prefix}}iam_scim_credentials (
    id BIGINT PRIMARY KEY,
    directory_id BIGINT NOT NULL REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    token_hash TEXT COLLATE "C" NOT NULL,
    expires_at TIMESTAMPTZ(3) NULL,
    last_used_at TIMESTAMPTZ(3) NULL,
    revoked_at TIMESTAMPTZ(3) NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_credentials_name
        CHECK (octet_length(name) >= 1 AND octet_length(name) <= 128)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_credentials_directory ON {{.Prefix}}iam_scim_credentials (directory_id, revoked_at, expires_at, created_at);

CREATE TABLE {{.Prefix}}iam_scim_resources (
    directory_id BIGINT NOT NULL REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    resource_type VARCHAR(16) COLLATE "C" NOT NULL CHECK (resource_type IN ('User', 'Group')),
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) COLLATE "C" NOT NULL DEFAULT '',
    external_key VARCHAR(512) COLLATE "C" NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    deleted_at TIMESTAMPTZ(3) NULL,
    PRIMARY KEY (directory_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_external
        CHECK (octet_length(external_id) <= 512 AND octet_length(external_key) >= 1 AND octet_length(external_key) <= 512),
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external UNIQUE (directory_id, resource_type, external_key)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_resources_list ON {{.Prefix}}iam_scim_resources (directory_id, resource_type, deleted_at, resource_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_resources;
DROP TABLE {{.Prefix}}iam_scim_credentials;
DROP TABLE {{.Prefix}}iam_scim_directories;
