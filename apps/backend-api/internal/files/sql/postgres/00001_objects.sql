-- +goose Up
CREATE TABLE {{.Prefix}}file_objects (
    id BIGINT NOT NULL PRIMARY KEY,
    backend_id VARCHAR(64) NOT NULL,
    storage_key VARCHAR(512) COLLATE "C" NOT NULL,
    uri TEXT NOT NULL,
    state VARCHAR(32) NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    md5 VARCHAR(32) NOT NULL,
    sha1 VARCHAR(40) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    sha512 VARCHAR(128) NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    file_type VARCHAR(64) NOT NULL,
    file_subtype VARCHAR(128) NOT NULL,
    format VARCHAR(64) NOT NULL DEFAULT '',
    width INTEGER NULL,
    height INTEGER NULL,
    metadata_json TEXT NOT NULL,
    metadata_state VARCHAR(32) NOT NULL,
    extractor_version VARCHAR(64) NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    UNIQUE (backend_id, storage_key)
);
CREATE INDEX {{.Prefix}}idx_file_objects_digest ON {{.Prefix}}file_objects (sha256, size, state);
CREATE INDEX {{.Prefix}}idx_file_objects_type ON {{.Prefix}}file_objects (file_type, file_subtype, created_at);
CREATE TABLE {{.Prefix}}file_account_references (
    id BIGINT NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    object_id BIGINT NOT NULL,
    purpose VARCHAR(32) NOT NULL,
    state VARCHAR(32) NOT NULL,
    expires_at BIGINT NULL,
    created_at BIGINT NOT NULL,
    FOREIGN KEY (object_id) REFERENCES {{.Prefix}}file_objects (id)
);
CREATE INDEX {{.Prefix}}idx_file_account_owner ON {{.Prefix}}file_account_references (principal_id, purpose, state);
CREATE INDEX {{.Prefix}}idx_file_account_object ON {{.Prefix}}file_account_references (object_id, state);
CREATE INDEX {{.Prefix}}idx_file_account_expiry ON {{.Prefix}}file_account_references (state, expires_at);
-- +goose Down
DROP TABLE {{.Prefix}}file_account_references;
DROP TABLE {{.Prefix}}file_objects;
