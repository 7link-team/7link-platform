-- Source: internal/modules/provisioning/sql/mysql/00002_scim_targets.sql:1-38.
-- Validation: internal/modules/provisioning/target.go:98-170,472-486.
-- The tenant reference is added by 00003_constraints.sql.
-- MySQL DDL auto-commits; each base version is independently recoverable.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_targets (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) COLLATE utf8mb4_bin NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    bearer_token_ciphertext TEXT COLLATE utf8mb4_bin NOT NULL,
    status VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_name
        CHECK (OCTET_LENGTH(name) >= 1 AND OCTET_LENGTH(name) <= 128),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_base_url
        CHECK (OCTET_LENGTH(base_url) >= 1 AND OCTET_LENGTH(base_url) <= 1024),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}uq_iam_scim_targets_name UNIQUE (tenant_id, name_key),
    KEY {{.Prefix}}idx_iam_scim_targets_tenant (tenant_id, status, name_key, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_target_resources (
    target_id BIGINT NOT NULL,
    resource_type VARCHAR(16) COLLATE utf8mb4_bin NOT NULL,
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    deleted_at DATETIME(3) NULL,
    PRIMARY KEY (target_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_type CHECK (resource_type IN ('User', 'Group')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_external
        CHECK (OCTET_LENGTH(external_id) <= 512),
    CONSTRAINT {{.Prefix}}fk_iam_scim_target_resources_target FOREIGN KEY (target_id) REFERENCES {{.Prefix}}iam_scim_targets(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_target_resources_target (target_id, resource_type, deleted_at, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_target_resources;
DROP TABLE {{.Prefix}}iam_scim_targets;
