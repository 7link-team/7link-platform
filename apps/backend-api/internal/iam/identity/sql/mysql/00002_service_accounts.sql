-- Source: internal/modules/iam/sql/mysql/00013_service_accounts.sql:2-33
-- MySQL DDL auto-commits; recovery treats both tables as one aggregate and drops children first.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_service_accounts (
    principal_id    BIGINT       NOT NULL PRIMARY KEY,
    owner_tenant_id BIGINT       NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:428-431.
    description     TEXT         NOT NULL DEFAULT (''),
    status          VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL DEFAULT 'active',
    expires_at      DATETIME(3)  NULL,
    token_version   BIGINT       NOT NULL DEFAULT 1,
    version         BIGINT       NOT NULL DEFAULT 1,
    created_at      DATETIME(3)  NOT NULL,
    updated_at      DATETIME(3)  NOT NULL,
    deleted_at      DATETIME(3)  NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_accounts_principal
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_service_accounts_status
        CHECK (status IN ('active', 'disabled', 'deleted')),
    INDEX {{.Prefix}}idx_iam_service_accounts_tenant
        (owner_tenant_id, status, principal_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_service_account_credentials (
    id           BIGINT       NOT NULL PRIMARY KEY,
    principal_id BIGINT       NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:304-327,434-451.
    name         VARCHAR(128) NOT NULL,
    secret_hash  TEXT         COLLATE utf8mb4_bin NOT NULL,
    scopes       TEXT         COLLATE utf8mb4_bin NOT NULL DEFAULT (''),
    expires_at   DATETIME(3)  NULL,
    last_used_at DATETIME(3)  NULL,
    revoked_at   DATETIME(3)  NULL,
    created_at   DATETIME(3)  NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_credentials_account
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE CASCADE,
    INDEX {{.Prefix}}idx_iam_service_credentials_active
        (principal_id, revoked_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_service_account_credentials;
DROP TABLE {{.Prefix}}iam_service_accounts;
