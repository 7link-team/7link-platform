-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:55-68
-- Validation: internal/modules/oauth/service.go:126-150,639-663.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_clients (
    id            BIGINT   NOT NULL PRIMARY KEY,
    tenant_id     BIGINT   NOT NULL,
    secret_hash   TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    name          TEXT     NOT NULL,
    redirect_uris TEXT     COLLATE BINARY NOT NULL DEFAULT '[]',
    grant_types   TEXT     COLLATE BINARY NOT NULL DEFAULT 'authorization_code,refresh_token',
    scopes        TEXT     COLLATE BINARY NOT NULL DEFAULT 'openid profile email',
    public_client INTEGER  NOT NULL DEFAULT 1,
    status        TEXT     COLLATE BINARY NOT NULL DEFAULT 'active',
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_oauth_clients_tenant_id UNIQUE (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_public CHECK (public_client IN (0, 1)),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_status CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX {{.Prefix}}idx_iam_oauth_clients_tenant
    ON {{.Prefix}}iam_oauth_clients (tenant_id, status, name);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_clients;
