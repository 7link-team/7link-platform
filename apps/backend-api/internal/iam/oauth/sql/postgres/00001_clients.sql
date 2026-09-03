-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:32-38
-- Validation: internal/modules/oauth/service.go:126-150,639-663.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_clients (
    id            BIGINT         NOT NULL PRIMARY KEY,
    tenant_id     BIGINT         NOT NULL,
    secret_hash   TEXT           COLLATE "C" NOT NULL DEFAULT '',
    name          VARCHAR(128)   NOT NULL,
    redirect_uris TEXT           COLLATE "C" NOT NULL DEFAULT '[]',
    grant_types   TEXT           COLLATE "C" NOT NULL DEFAULT 'authorization_code,refresh_token',
    scopes        TEXT           COLLATE "C" NOT NULL DEFAULT 'openid profile email',
    public_client BOOLEAN        NOT NULL DEFAULT TRUE,
    status        VARCHAR(16)    COLLATE "C" NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ(3) NOT NULL,
    updated_at    TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_oauth_clients_tenant_id UNIQUE (tenant_id, id),
    CONSTRAINT {{.Prefix}}chk_iam_oauth_clients_status CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX {{.Prefix}}idx_iam_oauth_clients_tenant
    ON {{.Prefix}}iam_oauth_clients (tenant_id, status, name);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_clients;
