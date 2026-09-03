-- Source: internal/modules/provisioning/sql/sqlite/00002_scim_targets.sql:1-34.
-- Validation: internal/modules/provisioning/target.go:98-170,472-486.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_targets (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    name_key TEXT COLLATE BINARY NOT NULL,
    base_url TEXT NOT NULL,
    bearer_token_ciphertext TEXT COLLATE BINARY NOT NULL,
    status TEXT COLLATE BINARY NOT NULL CHECK (status IN ('active', 'disabled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_name
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_base_url
        CHECK (length(CAST(base_url AS BLOB)) >= 1 AND length(CAST(base_url AS BLOB)) <= 1024),
    CONSTRAINT {{.Prefix}}uq_iam_scim_targets_name UNIQUE (tenant_id, name_key)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_targets_tenant ON {{.Prefix}}iam_scim_targets (tenant_id, status, name_key, id);

CREATE TABLE {{.Prefix}}iam_scim_target_resources (
    target_id BIGINT NOT NULL,
    resource_type TEXT COLLATE BINARY NOT NULL CHECK (resource_type IN ('User', 'Group')),
    resource_id BIGINT NOT NULL,
    external_id TEXT COLLATE BINARY NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    PRIMARY KEY (target_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_external
        CHECK (length(CAST(external_id AS BLOB)) <= 512),
    FOREIGN KEY (target_id) REFERENCES {{.Prefix}}iam_scim_targets (id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_scim_target_resources_target ON {{.Prefix}}iam_scim_target_resources (target_id, resource_type, deleted_at, resource_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_target_resources;
DROP TABLE {{.Prefix}}iam_scim_targets;
