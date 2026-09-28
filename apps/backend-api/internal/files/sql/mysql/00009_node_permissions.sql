-- +goose Up
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN root_scope VARCHAR(16) NULL;
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN root_owner_id BIGINT NULL;
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN acl_stop_inheritance BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE {{.Prefix}}file_nodes ADD CONSTRAINT {{.Prefix}}ck_file_nodes_root CHECK (
    (root_scope IS NULL AND root_owner_id IS NULL)
    OR (root_scope IN ('account', 'tenant', 'entity', 'department', 'personal') AND root_owner_id > 0 AND kind = 'directory' AND state = 'active')
);
CREATE UNIQUE INDEX {{.Prefix}}uq_file_nodes_root_owner ON {{.Prefix}}file_nodes (space_id, root_scope, root_owner_id);
CREATE INDEX {{.Prefix}}idx_file_nodes_roots ON {{.Prefix}}file_nodes (space_id, root_scope, state, id);
CREATE TABLE {{.Prefix}}file_node_acl (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    space_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    subject_kind VARCHAR(16) NOT NULL CHECK (subject_kind IN ('principal', 'department', 'group', 'role', 'owner')),
    subject_id BIGINT NOT NULL CHECK (subject_id > 0),
    effect VARCHAR(8) NOT NULL CHECK (effect IN ('allow', 'deny')),
    action VARCHAR(16) NOT NULL CHECK (action IN ('list', 'traverse', 'read', 'download', 'create', 'update', 'rename', 'move', 'delete', 'restore', 'purge', 'share', 'manage_acl')),
    applies VARCHAR(24) NOT NULL CHECK (applies IN ('self', 'descendants', 'self_and_descendants')),
    created_by BIGINT NOT NULL CHECK (created_by > 0),
    created_at BIGINT NOT NULL,
    UNIQUE (space_id, node_id, subject_kind, subject_id, effect, action, applies),
    FOREIGN KEY (space_id, node_id) REFERENCES {{.Prefix}}file_nodes (space_id, id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_file_node_acl_node ON {{.Prefix}}file_node_acl (space_id, node_id, id);
-- +goose Down
DROP TABLE {{.Prefix}}file_node_acl;
DROP INDEX {{.Prefix}}idx_file_nodes_roots ON {{.Prefix}}file_nodes;
DROP INDEX {{.Prefix}}uq_file_nodes_root_owner ON {{.Prefix}}file_nodes;
ALTER TABLE {{.Prefix}}file_nodes DROP CHECK {{.Prefix}}ck_file_nodes_root;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN acl_stop_inheritance;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN root_owner_id;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN root_scope;
