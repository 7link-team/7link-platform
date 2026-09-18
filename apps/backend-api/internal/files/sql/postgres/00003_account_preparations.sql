-- +goose Up
CREATE TABLE {{.Prefix}}file_account_preparations (
    id BIGINT NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    backend_id VARCHAR(64) NOT NULL,
    storage_key VARCHAR(512) COLLATE "C" NOT NULL,
    state VARCHAR(32) NOT NULL,
    expires_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    UNIQUE (backend_id, storage_key)
);
CREATE INDEX {{.Prefix}}idx_file_account_preparation_expiry ON {{.Prefix}}file_account_preparations (backend_id, state, expires_at, id);
-- +goose Down
DROP TABLE {{.Prefix}}file_account_preparations;
