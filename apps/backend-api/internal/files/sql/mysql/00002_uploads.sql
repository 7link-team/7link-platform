-- +goose Up
CREATE TABLE {{.Prefix}}file_uploads (
    id BIGINT NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    tenant_id BIGINT NULL,
    backend_id VARCHAR(64) NOT NULL,
    storage_key VARCHAR(512) NOT NULL,
    storage_handle TEXT NOT NULL,
    name VARCHAR(255) NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    committed_offset BIGINT NOT NULL,
    expected_md5 VARCHAR(32) NOT NULL,
    expected_sha1 VARCHAR(40) NOT NULL,
    expected_sha256 VARCHAR(64) NOT NULL,
    expected_sha512 VARCHAR(128) NOT NULL,
    checksum_state TEXT NOT NULL,
    state VARCHAR(32) NOT NULL,
    release_state VARCHAR(32) NOT NULL,
    release_error VARCHAR(64) NOT NULL DEFAULT '',
    last_progress_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    expired_at BIGINT NULL,
    lease_until BIGINT NOT NULL DEFAULT 0,
    fence BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    CONSTRAINT {{.Prefix}}chk_file_uploads_committed_offset
        CHECK (committed_offset >= 0 AND committed_offset <= size)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_file_upload_owner ON {{.Prefix}}file_uploads (tenant_id, principal_id, created_at, id);
CREATE INDEX {{.Prefix}}idx_file_upload_expiry ON {{.Prefix}}file_uploads (state, expires_at, id);
CREATE TABLE {{.Prefix}}file_upload_parts (
    id BIGINT NOT NULL PRIMARY KEY,
    upload_id BIGINT NOT NULL,
    part_index INTEGER NOT NULL CHECK (part_index > 0),
    start_offset BIGINT NOT NULL CHECK (start_offset >= 0),
    byte_length BIGINT NOT NULL CHECK (byte_length > 0),
    storage_token TEXT NOT NULL,
    state VARCHAR(32) NOT NULL,
    release_state VARCHAR(32) NOT NULL,
    created_at BIGINT NOT NULL,
    UNIQUE (upload_id, part_index),
    UNIQUE (upload_id, start_offset),
    FOREIGN KEY (upload_id) REFERENCES {{.Prefix}}file_uploads (id)
) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_file_upload_parts_state ON {{.Prefix}}file_upload_parts (upload_id, state, id);
-- +goose Down
DROP TABLE {{.Prefix}}file_upload_parts;
DROP TABLE {{.Prefix}}file_uploads;
