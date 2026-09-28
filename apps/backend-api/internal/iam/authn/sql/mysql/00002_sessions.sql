-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:15-21
-- Authentication-assurance columns: internal/modules/iam/sql/mysql/00019_authentication_assurance.sql:2-6.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_sessions (
    id_hash             CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id        BIGINT        NOT NULL,
    created_at          DATETIME(3)   NOT NULL,
    auth_time           DATETIME(3)   NOT NULL,
    acr                 INT           NOT NULL DEFAULT 0,
    amr                 TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    last_seen_at        DATETIME(3)   NOT NULL,
    expires_at          DATETIME(3)   NOT NULL,
    absolute_expires_at DATETIME(3)   NOT NULL,
    revoked_at          DATETIME(3)   NULL,
    ip_address          VARCHAR(64)   COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    user_agent          VARCHAR(512)  NOT NULL DEFAULT '',
    INDEX {{.Prefix}}idx_iam_sessions_principal (principal_id, revoked_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_sessions;
