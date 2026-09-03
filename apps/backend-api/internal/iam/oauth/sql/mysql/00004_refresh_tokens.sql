-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:22-28
-- Assurance fields: internal/modules/iam/sql/mysql/00019_authentication_assurance.sql:11-14.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_refresh_tokens (
    id_hash      CHAR(64)     COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    family_id    VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL,
    principal_id BIGINT       NOT NULL,
    client_id    BIGINT       NOT NULL,
    scope        TEXT         COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    auth_time    DATETIME(3)  NOT NULL,
    acr          INT          NOT NULL DEFAULT 0,
    amr          TEXT         COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    created_at   DATETIME(3)  NOT NULL,
    expires_at   DATETIME(3)  NOT NULL,
    used_at      DATETIME(3)  NULL,
    revoked_at   DATETIME(3)  NULL,
    INDEX {{.Prefix}}idx_iam_refresh_family (family_id, revoked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_refresh_tokens;
