-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
CREATE TABLE {{.Prefix}}file_spaces_v10 (
    id BIGINT NOT NULL PRIMARY KEY,
    scope_kind VARCHAR(16) NOT NULL CHECK (scope_kind IN ('system', 'tenant', 'account')),
    tenant_id BIGINT NULL,
    principal_id BIGINT NULL,
    system_slot INTEGER NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    CHECK ((scope_kind = 'system' AND tenant_id IS NULL AND principal_id IS NULL AND system_slot = 1)
        OR (scope_kind = 'tenant' AND tenant_id IS NOT NULL AND principal_id IS NULL AND system_slot IS NULL)
        OR (scope_kind = 'account' AND principal_id IS NOT NULL AND tenant_id IS NULL AND system_slot IS NULL)),
    UNIQUE (tenant_id),
    UNIQUE (principal_id),
    UNIQUE (system_slot)
);
INSERT INTO {{.Prefix}}file_spaces_v10 (id, scope_kind, tenant_id, principal_id, system_slot, version, created_at)
SELECT id, scope_kind, tenant_id, principal_id, NULL, version, created_at FROM {{.Prefix}}file_spaces;
DROP TABLE {{.Prefix}}file_spaces;
ALTER TABLE {{.Prefix}}file_spaces_v10 RENAME TO {{.Prefix}}file_spaces;
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN management_mode VARCHAR(16) NOT NULL DEFAULT 'user' CHECK (management_mode IN ('user', 'resource') AND (management_mode = 'user' OR kind = 'file'));
CREATE TABLE {{.Prefix}}file_resource_bindings (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    space_id BIGINT NOT NULL,
    file_id BIGINT NOT NULL,
    file_kind VARCHAR(16) NOT NULL DEFAULT 'file' CHECK (file_kind = 'file'),
    version_id BIGINT NULL,
    purpose VARCHAR(64) NOT NULL CHECK (length(purpose) > 0),
    resource_kind VARCHAR(64) NOT NULL CHECK (length(resource_kind) > 0),
    resource_id BIGINT NOT NULL CHECK (resource_id > 0),
    slot VARCHAR(64) NOT NULL CHECK (length(slot) > 0),
    state VARCHAR(16) NOT NULL CHECK (state IN ('active', 'inactive', 'expired')),
    live_slot INTEGER NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    expires_at BIGINT NULL,
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CHECK ((state = 'active' AND live_slot = 1) OR (state <> 'active' AND live_slot IS NULL)),
    UNIQUE (resource_kind, resource_id, purpose, slot, live_slot),
    FOREIGN KEY (space_id, file_id, file_kind) REFERENCES {{.Prefix}}file_nodes (space_id, id, kind),
    FOREIGN KEY (space_id, file_id, version_id) REFERENCES {{.Prefix}}file_versions (space_id, node_id, id)
);
CREATE INDEX {{.Prefix}}idx_file_resource_bindings_resource ON {{.Prefix}}file_resource_bindings (resource_kind, resource_id, purpose, state, id);
CREATE INDEX {{.Prefix}}idx_file_resource_bindings_file ON {{.Prefix}}file_resource_bindings (space_id, file_id, state, id);
CREATE INDEX {{.Prefix}}idx_file_resource_bindings_expiry ON {{.Prefix}}file_resource_bindings (state, expires_at, id);
CREATE INDEX {{.Prefix}}idx_file_nodes_management ON {{.Prefix}}file_nodes (management_mode, state, id, space_id);
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose Down
CREATE TEMPORARY TABLE {{.Prefix}}file_spaces_v10_down_guard (system_rows INTEGER NOT NULL CHECK (system_rows = 0));
INSERT INTO {{.Prefix}}file_spaces_v10_down_guard (system_rows) SELECT COUNT(*) FROM {{.Prefix}}file_spaces WHERE scope_kind = 'system';
DROP TABLE {{.Prefix}}file_spaces_v10_down_guard;
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
DROP TABLE {{.Prefix}}file_resource_bindings;
DROP INDEX {{.Prefix}}idx_file_nodes_management;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN management_mode;
CREATE TABLE {{.Prefix}}file_spaces_v9 (
    id BIGINT NOT NULL PRIMARY KEY,
    scope_kind VARCHAR(16) NOT NULL CHECK (scope_kind IN ('tenant', 'account')),
    tenant_id BIGINT NULL,
    principal_id BIGINT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    CHECK ((scope_kind = 'tenant' AND tenant_id IS NOT NULL AND principal_id IS NULL)
        OR (scope_kind = 'account' AND principal_id IS NOT NULL AND tenant_id IS NULL)),
    UNIQUE (tenant_id),
    UNIQUE (principal_id)
);
INSERT INTO {{.Prefix}}file_spaces_v9 (id, scope_kind, tenant_id, principal_id, version, created_at)
SELECT id, scope_kind, tenant_id, principal_id, version, created_at FROM {{.Prefix}}file_spaces;
DROP TABLE {{.Prefix}}file_spaces;
ALTER TABLE {{.Prefix}}file_spaces_v9 RENAME TO {{.Prefix}}file_spaces;
COMMIT;
PRAGMA foreign_keys = ON;
