-- Source: internal/modules/iam/sql/postgres/00013_service_accounts.sql:2-31
-- The two tables form one aggregate and are recovered together if MySQL auto-commit fails.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_service_accounts (
    principal_id   BIGINT         NOT NULL PRIMARY KEY,
    owner_tenant_id BIGINT        NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:428-431.
    description    TEXT           NOT NULL DEFAULT '',
    status         VARCHAR(16)    COLLATE "C" NOT NULL DEFAULT 'active',
    expires_at     TIMESTAMPTZ(3) NULL,
    token_version  BIGINT         NOT NULL DEFAULT 1,
    version        BIGINT         NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ(3) NOT NULL,
    updated_at     TIMESTAMPTZ(3) NOT NULL,
    deleted_at     TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_accounts_principal
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_principals (id) ON DELETE RESTRICT,
    CONSTRAINT {{.Prefix}}chk_iam_service_accounts_status
        CHECK (status IN ('active', 'disabled', 'deleted'))
);

CREATE INDEX {{.Prefix}}idx_iam_service_accounts_tenant
    ON {{.Prefix}}iam_service_accounts (owner_tenant_id, status, principal_id);

CREATE TABLE {{.Prefix}}iam_service_account_credentials (
    id           BIGINT         NOT NULL PRIMARY KEY,
    principal_id BIGINT         NOT NULL,
    -- Source validation: internal/modules/identity/service_account.go:304-327,434-451.
    name         VARCHAR(128)   NOT NULL,
    secret_hash  TEXT           COLLATE "C" NOT NULL,
    scopes       TEXT           COLLATE "C" NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ(3) NULL,
    last_used_at TIMESTAMPTZ(3) NULL,
    revoked_at   TIMESTAMPTZ(3) NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}fk_iam_service_credentials_account
        FOREIGN KEY (principal_id)
        REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE CASCADE
);

CREATE INDEX {{.Prefix}}idx_iam_service_credentials_active
    ON {{.Prefix}}iam_service_account_credentials (principal_id, revoked_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_service_account_credentials;
DROP TABLE {{.Prefix}}iam_service_accounts;
