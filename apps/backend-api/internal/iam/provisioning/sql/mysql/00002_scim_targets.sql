-- Source: internal/modules/provisioning/sql/mysql/00002_scim_targets.sql:1-38
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_targets (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    bearer_token_ciphertext TEXT NOT NULL,
    status VARCHAR(16) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_status CHECK (status IN ('active', 'disabled')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_version CHECK (version >= 1),
    CONSTRAINT {{.Prefix}}uq_iam_scim_targets_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_targets_tenant FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_targets_tenant (tenant_id, status, name_key, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_scim_target_resources (
    target_id BIGINT NOT NULL,
    resource_type VARCHAR(16) NOT NULL,
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    deleted_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (target_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_type CHECK (resource_type IN ('User', 'Group')),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_version CHECK (version >= 1 AND deleted_at >= 0),
    CONSTRAINT {{.Prefix}}fk_iam_scim_target_resources_target FOREIGN KEY (target_id) REFERENCES {{.Prefix}}iam_scim_targets(id) ON DELETE CASCADE,
    KEY {{.Prefix}}idx_iam_scim_target_resources_target (target_id, resource_type, deleted_at, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_target_resources;
DROP TABLE {{.Prefix}}iam_scim_targets;