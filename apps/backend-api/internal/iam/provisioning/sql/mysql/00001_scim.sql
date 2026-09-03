-- Source: internal/modules/provisioning/sql/mysql/00001_scim.sql:1-53
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_directories (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}uq_iam_scim_directories_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_directories_tenant FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_directories_tenant (tenant_id, status, name_key, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_credentials (
    id BIGINT NOT NULL PRIMARY KEY,
    directory_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    token_hash TEXT NOT NULL,
    expires_at BIGINT NOT NULL DEFAULT 0,
    last_used_at BIGINT NOT NULL DEFAULT 0,
    revoked_at BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_credentials_times CHECK (expires_at >= 0 AND last_used_at >= 0 AND revoked_at >= 0),
    CONSTRAINT {{.Prefix}}fk_iam_scim_credentials_directory FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_credentials_directory (directory_id, revoked_at, expires_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_resources (
    directory_id BIGINT NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) NOT NULL DEFAULT '',
    external_key VARCHAR(512) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    deleted_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (directory_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_type CHECK (resource_type IN ('User', 'Group')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_version CHECK (version >= 1 AND deleted_at >= 0),
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external UNIQUE (directory_id, resource_type, external_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_resources_directory FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_resources_list (directory_id, resource_type, deleted_at, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_resources;
DROP TABLE {{.Prefix}}iam_scim_credentials;
DROP TABLE {{.Prefix}}iam_scim_directories;