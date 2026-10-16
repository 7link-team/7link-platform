-- +goose Up
CREATE TABLE {{.Prefix}}platform_idempotency_keys (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL DEFAULT 0,
 subject_kind VARCHAR(16) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 subject_id BIGINT NOT NULL,
 operation VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 `key` VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 request_hash CHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 state VARCHAR(16) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
 status_code SMALLINT NULL,
 response_body MEDIUMTEXT NULL,
 resource_kind VARCHAR(32) NOT NULL DEFAULT '',
 resource_id BIGINT NULL,
 created_at DATETIME(3) NOT NULL,
 expires_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_platform_idempotency_keys UNIQUE (tenant_id,subject_kind,subject_id,operation,`key`),
 CONSTRAINT {{.Prefix}}chk_idempotency_subject CHECK (tenant_id >= 0 AND subject_id > 0 AND subject_kind IN ('PRINCIPAL','CONTACT')),
 CONSTRAINT {{.Prefix}}chk_idempotency_state CHECK (state IN ('IN_PROGRESS','COMPLETED')),
 CONSTRAINT {{.Prefix}}chk_idempotency_status CHECK (status_code IS NULL OR status_code BETWEEN 200 AND 599),
 CONSTRAINT {{.Prefix}}chk_idempotency_expiry CHECK (expires_at > created_at),
 CONSTRAINT {{.Prefix}}chk_idempotency_lengths CHECK (length(operation) BETWEEN 1 AND 128 AND length(`key`) BETWEEN 1 AND 64 AND length(request_hash)=64 AND length(resource_kind)<=32)
);
CREATE INDEX {{.Prefix}}idx_platform_idempotency_keys_expiry ON {{.Prefix}}platform_idempotency_keys(expires_at);
-- +goose Down
DROP TABLE {{.Prefix}}platform_idempotency_keys;
