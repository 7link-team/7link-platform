-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/provisioning/sql/sqlite/00001_scim.sql:13;
-- internal/modules/provisioning/sql/sqlite/00002_scim_targets.sql:13.
-- SQLite cannot add or drop foreign keys in place, so each tenant-owned parent
-- is rebuilt together with its dependent tables.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_credentials
    RENAME TO {{.Prefix}}iam_scim_credentials_old;
ALTER TABLE {{.Prefix}}iam_scim_resources
    RENAME TO {{.Prefix}}iam_scim_resources_old;
ALTER TABLE {{.Prefix}}iam_scim_directories
    RENAME TO {{.Prefix}}iam_scim_directories_old;
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
    CONSTRAINT {{.Prefix}}uq_iam_scim_directories_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_directories_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_directories
SELECT * FROM {{.Prefix}}iam_scim_directories_old;
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
    CONSTRAINT {{.Prefix}}fk_iam_scim_credentials_directory
        FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_credentials
SELECT * FROM {{.Prefix}}iam_scim_credentials_old;
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
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external
        UNIQUE (directory_id, resource_type, external_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_resources_directory
        FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_resources
SELECT * FROM {{.Prefix}}iam_scim_resources_old;
DROP TABLE {{.Prefix}}iam_scim_credentials_old;
DROP TABLE {{.Prefix}}iam_scim_resources_old;
DROP TABLE {{.Prefix}}iam_scim_directories_old;
CREATE INDEX {{.Prefix}}idx_iam_scim_directories_tenant
    ON {{.Prefix}}iam_scim_directories (tenant_id, status, name_key, id);
CREATE INDEX {{.Prefix}}idx_iam_scim_credentials_directory
    ON {{.Prefix}}iam_scim_credentials (directory_id, revoked_at, expires_at, created_at);
CREATE INDEX {{.Prefix}}idx_iam_scim_resources_list
    ON {{.Prefix}}iam_scim_resources (directory_id, resource_type, deleted_at, resource_id);

ALTER TABLE {{.Prefix}}iam_scim_target_resources
    RENAME TO {{.Prefix}}iam_scim_target_resources_old;
ALTER TABLE {{.Prefix}}iam_scim_targets
    RENAME TO {{.Prefix}}iam_scim_targets_old;
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
    CONSTRAINT {{.Prefix}}uq_iam_scim_targets_name UNIQUE (tenant_id, name_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_targets_tenant
        FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_targets
SELECT * FROM {{.Prefix}}iam_scim_targets_old;
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
    CONSTRAINT {{.Prefix}}fk_iam_scim_target_resources_target
        FOREIGN KEY (target_id) REFERENCES {{.Prefix}}iam_scim_targets (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_target_resources
SELECT * FROM {{.Prefix}}iam_scim_target_resources_old;
DROP TABLE {{.Prefix}}iam_scim_target_resources_old;
DROP TABLE {{.Prefix}}iam_scim_targets_old;
CREATE INDEX {{.Prefix}}idx_iam_scim_targets_tenant
    ON {{.Prefix}}iam_scim_targets (tenant_id, status, name_key, id);
CREATE INDEX {{.Prefix}}idx_iam_scim_target_resources_target
    ON {{.Prefix}}iam_scim_target_resources (target_id, resource_type, deleted_at, resource_id);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_scim_target_resources
    RENAME TO {{.Prefix}}iam_scim_target_resources_old;
ALTER TABLE {{.Prefix}}iam_scim_targets
    RENAME TO {{.Prefix}}iam_scim_targets_old;
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
INSERT INTO {{.Prefix}}iam_scim_targets
SELECT * FROM {{.Prefix}}iam_scim_targets_old;
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
    CONSTRAINT {{.Prefix}}fk_iam_scim_target_resources_target
        FOREIGN KEY (target_id) REFERENCES {{.Prefix}}iam_scim_targets (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_target_resources
SELECT * FROM {{.Prefix}}iam_scim_target_resources_old;
DROP TABLE {{.Prefix}}iam_scim_target_resources_old;
DROP TABLE {{.Prefix}}iam_scim_targets_old;
CREATE INDEX {{.Prefix}}idx_iam_scim_targets_tenant
    ON {{.Prefix}}iam_scim_targets (tenant_id, status, name_key, id);
CREATE INDEX {{.Prefix}}idx_iam_scim_target_resources_target
    ON {{.Prefix}}iam_scim_target_resources (target_id, resource_type, deleted_at, resource_id);

ALTER TABLE {{.Prefix}}iam_scim_credentials
    RENAME TO {{.Prefix}}iam_scim_credentials_old;
ALTER TABLE {{.Prefix}}iam_scim_resources
    RENAME TO {{.Prefix}}iam_scim_resources_old;
ALTER TABLE {{.Prefix}}iam_scim_directories
    RENAME TO {{.Prefix}}iam_scim_directories_old;
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
INSERT INTO {{.Prefix}}iam_scim_directories
SELECT * FROM {{.Prefix}}iam_scim_directories_old;
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
    CONSTRAINT {{.Prefix}}fk_iam_scim_credentials_directory
        FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_credentials
SELECT * FROM {{.Prefix}}iam_scim_credentials_old;
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
    CONSTRAINT {{.Prefix}}uq_iam_scim_resources_external
        UNIQUE (directory_id, resource_type, external_key),
    CONSTRAINT {{.Prefix}}fk_iam_scim_resources_directory
        FOREIGN KEY (directory_id) REFERENCES {{.Prefix}}iam_scim_directories (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_scim_resources
SELECT * FROM {{.Prefix}}iam_scim_resources_old;
DROP TABLE {{.Prefix}}iam_scim_credentials_old;
DROP TABLE {{.Prefix}}iam_scim_resources_old;
DROP TABLE {{.Prefix}}iam_scim_directories_old;
CREATE INDEX {{.Prefix}}idx_iam_scim_directories_tenant
    ON {{.Prefix}}iam_scim_directories (tenant_id, status, name_key, id);
CREATE INDEX {{.Prefix}}idx_iam_scim_credentials_directory
    ON {{.Prefix}}iam_scim_credentials (directory_id, revoked_at, expires_at, created_at);
CREATE INDEX {{.Prefix}}idx_iam_scim_resources_list
    ON {{.Prefix}}iam_scim_resources (directory_id, resource_type, deleted_at, resource_id);
