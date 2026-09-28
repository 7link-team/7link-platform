-- +goose Up
CREATE TABLE {{.Prefix}}file_versions (
    id BIGINT NOT NULL PRIMARY KEY,
    space_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    node_kind VARCHAR(16) NOT NULL DEFAULT 'file' CHECK (node_kind = 'file'),
    version_number BIGINT NOT NULL CHECK (version_number > 0),
    object_id BIGINT NOT NULL,
    ext VARCHAR(255) NOT NULL DEFAULT '',
    declared_mime VARCHAR(128) NOT NULL DEFAULT '',
    created_by BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    source_version_id BIGINT NULL,
    UNIQUE (space_id, id),
    UNIQUE (space_id, node_id, id),
    UNIQUE (space_id, node_id, version_number),
    FOREIGN KEY (space_id, node_id, node_kind) REFERENCES {{.Prefix}}file_nodes (space_id, id, kind),
    FOREIGN KEY (object_id) REFERENCES {{.Prefix}}file_objects (id),
    FOREIGN KEY (space_id, source_version_id) REFERENCES {{.Prefix}}file_versions (space_id, id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_file_versions_object ON {{.Prefix}}file_versions (object_id, node_id);
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN current_version_id BIGINT NULL;
ALTER TABLE {{.Prefix}}file_nodes ADD CONSTRAINT {{.Prefix}}fk_file_node_head FOREIGN KEY (space_id, id, current_version_id) REFERENCES {{.Prefix}}file_versions (space_id, node_id, id);
ALTER TABLE {{.Prefix}}file_nodes ADD CONSTRAINT {{.Prefix}}ck_file_node_head CHECK (kind = 'file' OR current_version_id IS NULL);
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_version_immutable BEFORE UPDATE ON {{.Prefix}}file_versions FOR EACH ROW
BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'file versions are immutable'; END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER {{.Prefix}}file_version_immutable;
ALTER TABLE {{.Prefix}}file_nodes DROP CHECK {{.Prefix}}ck_file_node_head;
ALTER TABLE {{.Prefix}}file_nodes DROP FOREIGN KEY {{.Prefix}}fk_file_node_head;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN current_version_id;
DROP TABLE {{.Prefix}}file_versions;
