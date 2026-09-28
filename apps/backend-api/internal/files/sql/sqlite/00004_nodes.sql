-- +goose Up
CREATE TABLE {{.Prefix}}file_spaces (
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
CREATE TABLE {{.Prefix}}file_nodes (
    id BIGINT NOT NULL PRIMARY KEY,
    space_id BIGINT NOT NULL,
    parent_id BIGINT NULL,
    parent_kind VARCHAR(16) NOT NULL DEFAULT 'directory' CHECK (parent_kind = 'directory'),
    root_slot INTEGER NULL,
    live_slot INTEGER NULL,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('directory', 'file')),
    name VARCHAR(255) COLLATE BINARY NOT NULL,
    name_key VARCHAR(768) COLLATE BINARY NOT NULL,
    ext VARCHAR(255) NOT NULL DEFAULT '',
    state VARCHAR(16) NOT NULL CHECK (state IN ('active', 'trashed')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CHECK ((parent_id IS NULL AND root_slot IS NOT NULL AND root_slot = 1 AND kind = 'directory' AND name = '' AND name_key = '' AND state = 'active')
        OR (parent_id IS NOT NULL AND root_slot IS NULL AND length(name) > 0 AND length(name_key) > 0)),
    CHECK ((state = 'active' AND live_slot IS NOT NULL AND live_slot = 1) OR (state = 'trashed' AND live_slot IS NULL)),
    CHECK (parent_id IS NULL OR parent_id <> id),
    UNIQUE (space_id, id),
    UNIQUE (space_id, id, kind),
    UNIQUE (space_id, root_slot),
    UNIQUE (space_id, parent_id, name_key, live_slot),
    FOREIGN KEY (space_id) REFERENCES {{.Prefix}}file_spaces (id),
    FOREIGN KEY (space_id, parent_id, parent_kind) REFERENCES {{.Prefix}}file_nodes (space_id, id, kind)
);
CREATE INDEX {{.Prefix}}idx_file_nodes_children ON {{.Prefix}}file_nodes (space_id, parent_id, state, name_key, id);
CREATE INDEX {{.Prefix}}idx_file_nodes_children_kind ON {{.Prefix}}file_nodes (space_id, parent_id, state, kind, name_key, id);
CREATE TABLE {{.Prefix}}file_node_closure (
    space_id BIGINT NOT NULL,
    ancestor_id BIGINT NOT NULL,
    descendant_id BIGINT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    PRIMARY KEY (space_id, ancestor_id, descendant_id),
    CHECK ((ancestor_id = descendant_id AND depth = 0) OR (ancestor_id <> descendant_id AND depth > 0)),
    FOREIGN KEY (space_id, ancestor_id) REFERENCES {{.Prefix}}file_nodes (space_id, id),
    FOREIGN KEY (space_id, descendant_id) REFERENCES {{.Prefix}}file_nodes (space_id, id)
);
CREATE INDEX {{.Prefix}}idx_file_node_closure_ancestors ON {{.Prefix}}file_node_closure (space_id, descendant_id, depth, ancestor_id);
CREATE INDEX {{.Prefix}}idx_file_node_closure_descendants ON {{.Prefix}}file_node_closure (space_id, ancestor_id, depth, descendant_id);
-- +goose Down
DROP TABLE {{.Prefix}}file_node_closure;
DROP TABLE {{.Prefix}}file_nodes;
DROP TABLE {{.Prefix}}file_spaces;
