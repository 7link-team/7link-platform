-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:29-34
-- Validation: internal/modules/oauth/service.go:126-150,639-663.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_clients (
    id            BIGINT        NOT NULL PRIMARY KEY,
    tenant_id     BIGINT        NOT NULL,
    secret_hash   TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    name          VARCHAR(128)  NOT NULL,
    redirect_uris TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT ('[]'),
    grant_types   TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT ('authorization_code,refresh_token'),
    scopes        TEXT          COLLATE utf8mb4_bin NOT NULL DEFAULT ('openid profile email'),
    public_client BOOLEAN       NOT NULL DEFAULT TRUE,
    status        VARCHAR(16)   COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    created_at    DATETIME(3)   NOT NULL,
    updated_at    DATETIME(3)   NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_oauth_clients_tenant_id UNIQUE (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_status CHECK (status IN ('active', 'disabled')),
    INDEX {{.Prefix}}idx_iam_oauth_clients_tenant (tenant_id, status, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_clients;
