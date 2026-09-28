-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:35-41
-- Assurance fields: internal/modules/iam/sql/mysql/00019_authentication_assurance.sql:7-10.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_codes (
    code_hash      CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    client_id      BIGINT        NOT NULL,
    principal_id   BIGINT        NOT NULL,
    redirect_uri   TEXT          COLLATE utf8mb4_bin NOT NULL,
    scope          TEXT          COLLATE utf8mb4_bin NOT NULL,
    code_challenge VARCHAR(128)  COLLATE utf8mb4_bin NOT NULL,
    nonce          VARCHAR(256)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    auth_time      DATETIME(3)   NOT NULL,
    acr            INT           NOT NULL DEFAULT 0,
    amr            TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    created_at     DATETIME(3)   NOT NULL,
    expires_at     DATETIME(3)   NOT NULL,
    consumed_at    DATETIME(3)   NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_codes;
