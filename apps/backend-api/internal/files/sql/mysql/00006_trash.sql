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
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN trash_batch_id BIGINT NULL;
ALTER TABLE {{.Prefix}}file_nodes ADD CONSTRAINT {{.Prefix}}fk_file_node_trash FOREIGN KEY (space_id, trash_batch_id) REFERENCES {{.Prefix}}file_trash_batches (space_id, id);
ALTER TABLE {{.Prefix}}file_nodes ADD CONSTRAINT {{.Prefix}}ck_file_node_trash CHECK (trash_batch_id IS NULL OR state = 'trashed');
CREATE INDEX {{.Prefix}}idx_file_nodes_trash_batch ON {{.Prefix}}file_nodes (space_id, trash_batch_id, id);
-- +goose Down
ALTER TABLE {{.Prefix}}file_nodes DROP FOREIGN KEY {{.Prefix}}fk_file_node_trash;
DROP INDEX {{.Prefix}}idx_file_nodes_trash_batch ON {{.Prefix}}file_nodes;
ALTER TABLE {{.Prefix}}file_nodes DROP CHECK {{.Prefix}}ck_file_node_trash;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN trash_batch_id;
DROP TABLE {{.Prefix}}file_trash_roots;
DROP TABLE {{.Prefix}}file_trash_batches;
