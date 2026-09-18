-- +goose Up
CREATE TABLE {{.Prefix}}file_trash_batches (
    id BIGINT NOT NULL PRIMARY KEY,
    space_id BIGINT NOT NULL,
    deleted_by BIGINT NOT NULL,
    deleted_at BIGINT NOT NULL,
    state VARCHAR(16) NOT NULL CHECK (state IN ('active', 'restored', 'purged')),
    node_count INTEGER NOT NULL CHECK (node_count > 0),
    root_count INTEGER NOT NULL CHECK (root_count > 0 AND root_count <= node_count),
    restored_by BIGINT NULL,
    restored_at BIGINT NULL,
    CHECK ((state = 'restored' AND restored_by IS NOT NULL AND restored_at IS NOT NULL)
        OR (state <> 'restored' AND restored_by IS NULL AND restored_at IS NULL)),
    UNIQUE (space_id, id),
    FOREIGN KEY (space_id) REFERENCES {{.Prefix}}file_spaces (id)
);
CREATE INDEX {{.Prefix}}idx_file_trash_batches_list ON {{.Prefix}}file_trash_batches (space_id, state, deleted_at, id);
CREATE TABLE {{.Prefix}}file_trash_roots (
    space_id BIGINT NOT NULL,
    batch_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    parent_id BIGINT NOT NULL,
    parent_kind VARCHAR(16) NOT NULL CHECK (parent_kind = 'directory'),
    name VARCHAR(255) NOT NULL,
    name_key VARCHAR(768) NOT NULL,
    ext VARCHAR(255) NOT NULL DEFAULT '',
    PRIMARY KEY (space_id, batch_id, node_id),
    CHECK (node_id <> parent_id),
    FOREIGN KEY (space_id, batch_id) REFERENCES {{.Prefix}}file_trash_batches (space_id, id),
    FOREIGN KEY (space_id, node_id) REFERENCES {{.Prefix}}file_nodes (space_id, id),
    FOREIGN KEY (space_id, parent_id, parent_kind) REFERENCES {{.Prefix}}file_nodes (space_id, id, kind)
);
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN trash_batch_id BIGINT NULL REFERENCES {{.Prefix}}file_trash_batches (id);
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_node_trash_insert BEFORE INSERT ON {{.Prefix}}file_nodes
WHEN NEW.trash_batch_id IS NOT NULL AND (NEW.state <> 'trashed' OR NOT EXISTS (
    SELECT 1 FROM {{.Prefix}}file_trash_batches b WHERE b.id = NEW.trash_batch_id AND b.space_id = NEW.space_id
)) BEGIN SELECT RAISE(ABORT, 'trash batch must belong to node space and node must be trashed'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_node_trash_update BEFORE UPDATE ON {{.Prefix}}file_nodes
WHEN NEW.trash_batch_id IS NOT NULL AND (NEW.state <> 'trashed' OR NOT EXISTS (
    SELECT 1 FROM {{.Prefix}}file_trash_batches b WHERE b.id = NEW.trash_batch_id AND b.space_id = NEW.space_id
)) BEGIN SELECT RAISE(ABORT, 'trash batch must belong to node space and node must be trashed'); END;
-- +goose StatementEnd
CREATE INDEX {{.Prefix}}idx_file_nodes_trash_batch ON {{.Prefix}}file_nodes (space_id, trash_batch_id, id);
-- +goose Down
DROP TRIGGER {{.Prefix}}file_node_trash_update;
DROP TRIGGER {{.Prefix}}file_node_trash_insert;
DROP INDEX {{.Prefix}}idx_file_nodes_trash_batch;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN trash_batch_id;
DROP TABLE {{.Prefix}}file_trash_roots;
DROP TABLE {{.Prefix}}file_trash_batches;
