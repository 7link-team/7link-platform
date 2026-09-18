-- +goose Up
CREATE INDEX {{.Prefix}}idx_file_nodes_children_cursor ON {{.Prefix}}file_nodes (space_id, parent_id, state, id);
CREATE TABLE {{.Prefix}}file_operations (
    id BIGINT NOT NULL PRIMARY KEY,
    space_id BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    idempotency_key VARCHAR(128) COLLATE "C" NOT NULL,
    request_hash CHAR(64) NOT NULL,
    request_json TEXT NOT NULL,
    roots_json TEXT NOT NULL,
    action VARCHAR(16) NOT NULL CHECK (action IN ('move', 'copy')),
    state VARCHAR(32) NOT NULL CHECK (state IN ('queued', 'preparing', 'awaiting_decision', 'ready', 'committing', 'succeeded', 'failed', 'cancelled')),
    revision BIGINT NOT NULL CHECK (revision > 0),
    space_revision BIGINT NOT NULL CHECK (space_revision > 0),
    destination_id BIGINT NOT NULL,
    destination_version BIGINT NOT NULL CHECK (destination_version > 0),
    item_count INTEGER NOT NULL CHECK (item_count >= 0),
    node_budget INTEGER NOT NULL CHECK (node_budget > 0),
    closure_budget INTEGER NOT NULL CHECK (closure_budget > 0),
    result_json TEXT NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    completed_at BIGINT NULL,
    CHECK (expires_at > created_at),
    CHECK ((state IN ('succeeded', 'failed', 'cancelled') AND completed_at IS NOT NULL)
        OR (state NOT IN ('succeeded', 'failed', 'cancelled') AND completed_at IS NULL)),
    UNIQUE (space_id, id),
    UNIQUE (space_id, actor_id, idempotency_key),
    FOREIGN KEY (space_id) REFERENCES {{.Prefix}}file_spaces (id)
);
CREATE INDEX {{.Prefix}}idx_file_operations_list ON {{.Prefix}}file_operations (space_id, actor_id, created_at, id);
CREATE INDEX {{.Prefix}}idx_file_operations_expiry ON {{.Prefix}}file_operations (state, expires_at, id);
CREATE UNIQUE INDEX {{.Prefix}}idx_file_versions_plan_pin ON {{.Prefix}}file_versions (space_id, node_id, id, object_id);
CREATE TABLE {{.Prefix}}file_operation_items (
    id BIGINT NOT NULL PRIMARY KEY,
    space_id BIGINT NOT NULL,
    operation_id BIGINT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    node_id BIGINT NOT NULL,
    snapshot_json TEXT NOT NULL,
    pinned_node_id BIGINT NULL,
    pinned_version_id BIGINT NULL,
    pinned_object_id BIGINT NULL,
    CHECK (pinned_node_id IS NULL OR pinned_node_id = node_id),
    CHECK ((pinned_version_id IS NULL AND pinned_object_id IS NULL)
        OR (pinned_version_id IS NOT NULL AND pinned_object_id IS NOT NULL AND pinned_node_id IS NOT NULL)),
    UNIQUE (operation_id, ordinal),
    UNIQUE (operation_id, node_id),
    FOREIGN KEY (space_id, operation_id) REFERENCES {{.Prefix}}file_operations (space_id, id),
    FOREIGN KEY (space_id, pinned_node_id) REFERENCES {{.Prefix}}file_nodes (space_id, id),
    FOREIGN KEY (space_id, pinned_node_id, pinned_version_id, pinned_object_id) REFERENCES {{.Prefix}}file_versions (space_id, node_id, id, object_id),
    FOREIGN KEY (pinned_object_id) REFERENCES {{.Prefix}}file_objects (id)
);
CREATE INDEX {{.Prefix}}idx_file_operation_items_object ON {{.Prefix}}file_operation_items (pinned_object_id, operation_id);
-- +goose Down
DROP INDEX {{.Prefix}}idx_file_nodes_children_cursor;
DROP TABLE {{.Prefix}}file_operation_items;
DROP INDEX {{.Prefix}}idx_file_versions_plan_pin;
DROP TABLE {{.Prefix}}file_operations;
