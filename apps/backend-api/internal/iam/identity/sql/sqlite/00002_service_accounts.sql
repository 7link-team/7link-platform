-- Source: internal/modules/iam/sql/sqlite/00013_service_accounts.sql:2-33
-- The two tables form one aggregate and are recovered together if a migration is interrupted.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_service_accounts (
    principal_id    BIGINT   NOT NULL PRIMARY KEY,
    owner_tenant_id BIGINT   NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:428-431.
    description     TEXT     NOT NULL DEFAULT '',
    status          TEXT     COLLATE BINARY NOT NULL DEFAULT 'active',
    expires_at      DATETIME NULL,
    token_version   BIGINT   NOT NULL DEFAULT 1,
    version         BIGINT   NOT NULL DEFAULT 1,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    deleted_at      DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_accounts_principal
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_service_accounts_status
        CHECK (status IN ('active', 'disabled', 'deleted'))
);

CREATE INDEX {{.Prefix}}idx_iam_service_accounts_tenant
    ON {{.Prefix}}iam_service_accounts (owner_tenant_id, status, principal_id);

CREATE TABLE {{.Prefix}}iam_service_account_credentials (
    id           BIGINT   NOT NULL PRIMARY KEY,
    principal_id BIGINT   NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:304-327,434-451.
    name         TEXT     NOT NULL,
    secret_hash  TEXT     COLLATE BINARY NOT NULL,
    scopes       TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    expires_at   DATETIME NULL,
    last_used_at DATETIME NULL,
    revoked_at   DATETIME NULL,
    created_at   DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_credentials_account
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE CASCADE
);

CREATE INDEX {{.Prefix}}idx_iam_service_credentials_active
    ON {{.Prefix}}iam_service_account_credentials (principal_id, revoked_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_service_account_credentials;
DROP TABLE {{.Prefix}}iam_service_accounts;
