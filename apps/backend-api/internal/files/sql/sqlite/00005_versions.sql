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
);
CREATE INDEX {{.Prefix}}idx_file_versions_object ON {{.Prefix}}file_versions (object_id, node_id);
ALTER TABLE {{.Prefix}}file_nodes ADD COLUMN current_version_id BIGINT NULL REFERENCES {{.Prefix}}file_versions (id);
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_node_head_insert BEFORE INSERT ON {{.Prefix}}file_nodes
WHEN NEW.current_version_id IS NOT NULL AND (NEW.kind <> 'file' OR NOT EXISTS (
    SELECT 1 FROM {{.Prefix}}file_versions v WHERE v.id = NEW.current_version_id AND v.space_id = NEW.space_id AND v.node_id = NEW.id
)) BEGIN SELECT RAISE(ABORT, 'file head must belong to its node and space'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_node_head_update BEFORE UPDATE ON {{.Prefix}}file_nodes
WHEN NEW.current_version_id IS NOT NULL AND (NEW.kind <> 'file' OR NOT EXISTS (
    SELECT 1 FROM {{.Prefix}}file_versions v WHERE v.id = NEW.current_version_id AND v.space_id = NEW.space_id AND v.node_id = NEW.id
)) BEGIN SELECT RAISE(ABORT, 'file head must belong to its node and space'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}file_version_immutable BEFORE UPDATE ON {{.Prefix}}file_versions
BEGIN SELECT RAISE(ABORT, 'file versions are immutable'); END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER {{.Prefix}}file_version_immutable;
DROP TRIGGER {{.Prefix}}file_node_head_update;
DROP TRIGGER {{.Prefix}}file_node_head_insert;
UPDATE {{.Prefix}}file_nodes SET current_version_id = NULL;
ALTER TABLE {{.Prefix}}file_nodes DROP COLUMN current_version_id;
DROP TABLE {{.Prefix}}file_versions;
