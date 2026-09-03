-- Source: internal/modules/provisioning/sql/mysql/00001_scim.sql:1-53.
-- Validation: internal/modules/provisioning/service.go:96-201;
-- internal/modules/provisioning/scim.go:217-270.
-- The tenant reference is added by 00003_constraints.sql.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_directories (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
    status VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_name
        CHECK (OCTET_LENGTH(name) >= 1 AND OCTET_LENGTH(name) <= 128),
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}uq_iam_scim_directories_name UNIQUE (tenant_id, name_key),
    KEY {{.Prefix}}idx_iam_scim_directories_tenant (tenant_id, status, name_key, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_credentials (
    id BIGINT NOT NULL PRIMARY KEY,
    directory_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    token_hash TEXT COLLATE utf8mb4_bin NOT NULL,
    expires_at DATETIME(3) NULL,
    last_used_at DATETIME(3) NULL,
    revoked_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_credentials_name
        CHECK (OCTET_LENGTH(name) >= 1 AND OCTET_LENGTH(name) <= 128),
    CONSTRAINT {{.Prefix}}fk_iam_scim_credentials_directory FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_credentials_directory (directory_id, revoked_at, expires_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_resources (
    directory_id BIGINT NOT NULL,
    resource_type VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    external_key VARCHAR(512) COLLATE utf8mb4_bin NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    deleted_at DATETIME(3) NULL,
    PRIMARY KEY (directory_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_type CHECK (resource_type IN ('User', 'Group')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_external
        CHECK (OCTET_LENGTH(external_id) <= 512 AND OCTET_LENGTH(external_key) >= 1 AND OCTET_LENGTH(external_key) <= 512),
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external UNIQUE (directory_id, resource_type, external_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_resources_directory FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_resources_list (directory_id, resource_type, deleted_at, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_resources;
DROP TABLE {{.Prefix}}iam_scim_credentials;
DROP TABLE {{.Prefix}}iam_scim_directories;
