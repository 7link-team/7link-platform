-- Source: internal/modules/provisioning/sql/sqlite/00001_scim.sql:1-48.
-- Validation: internal/modules/provisioning/service.go:96-201;
-- internal/modules/provisioning/scim.go:217-270.
-- The tenant reference is added by 00003_constraints.sql.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_scim_directories (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    name_key TEXT COLLATE BINARY NOT NULL,
    status TEXT COLLATE BINARY NOT NULL CHECK (status IN ('active', 'disabled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_directories_name
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128),
    CONSTRAINT {{.Prefix}}uq_iam_scim_directories_name UNIQUE (tenant_id, name_key)
);
CREATE INDEX {{.Prefix}}idx_iam_scim_directories_tenant ON {{.Prefix}}iam_scim_directories (tenant_id, status, name_key, id);

CREATE TABLE {{.Prefix}}iam_scim_credentials (
    id BIGINT NOT NULL PRIMARY KEY,
    directory_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT COLLATE BINARY NOT NULL,
    expires_at DATETIME NULL,
    last_used_at DATETIME NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_scim_credentials_name
        CHECK (length(CAST(name AS BLOB)) >= 1 AND length(CAST(name AS BLOB)) <= 128),
    FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_scim_credentials_directory ON {{.Prefix}}iam_scim_credentials (directory_id, revoked_at, expires_at, created_at);

CREATE TABLE {{.Prefix}}iam_scim_resources (
    directory_id BIGINT NOT NULL,
    resource_type TEXT COLLATE BINARY NOT NULL CHECK (resource_type IN ('User', 'Group')),
    resource_id BIGINT NOT NULL,
    external_id TEXT COLLATE BINARY NOT NULL DEFAULT '',
    external_key TEXT COLLATE BINARY NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    PRIMARY KEY (directory_id, resource_type, resource_id),
    CONSTRAINT {{.Prefix}}chk_iam_scim_resources_external
        CHECK (length(CAST(external_id AS BLOB)) <= 512 AND length(CAST(external_key AS BLOB)) >= 1 AND length(CAST(external_key AS BLOB)) <= 512),
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external UNIQUE (directory_id, resource_type, external_key),
    FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_iam_scim_resources_list ON {{.Prefix}}iam_scim_resources (directory_id, resource_type, deleted_at, resource_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_scim_resources;
DROP TABLE {{.Prefix}}iam_scim_credentials;
DROP TABLE {{.Prefix}}iam_scim_directories;
