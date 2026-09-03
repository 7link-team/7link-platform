-- Source: internal/modules/provisioning/sql/postgres/00002_scim_targets.sql:1-32.
-- Validation: internal/modules/provisioning/target.go:98-170,472-486.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_targets (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    name_key VARCHAR(128) COLLATE "C" NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    bearer_token_ciphertext TEXT COLLATE "C" NOT NULL,
    status VARCHAR(16) COLLATE "C" NOT NULL CHECK (status IN ('active', 'disabled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_name
        CHECK (octet_length(name) >= 1 AND octet_length(name) <= 128),
    CONSTRAINT {{.Prefix}}chk_iam_scim_targets_base_url
        CHECK (octet_length(base_url) >= 1 AND octet_length(base_url) <= 1024),
    CONSTRAINT {{.Prefix}}uq_iam_scim_targets_name UNIQUE (tenant_id, name_key)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_targets_tenant ON {{.Prefix}}iam_scim_targets (tenant_id, status, name_key, id);

CREATE TABLE {{.Prefix}}iam_scim_target_resources (
    target_id BIGINT NOT NULL REFERENCES {{.Prefix}}iam_scim_targets(id) ON DELETE CASCADE,
    resource_type VARCHAR(16) COLLATE "C" NOT NULL CHECK (resource_type IN ('User', 'Group')),
    resource_id BIGINT NOT NULL,
    external_id VARCHAR(512) COLLATE "C" NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    deleted_at TIMESTAMPTZ(3) NULL,
    PRIMARY KEY (target_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_target_resources_external
        CHECK (octet_length(external_id) <= 512)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_target_resources_target ON {{.Prefix}}iam_scim_target_resources (target_id, resource_type, deleted_at, resource_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_target_resources;
DROP TABLE {{.Prefix}}iam_scim_targets;
