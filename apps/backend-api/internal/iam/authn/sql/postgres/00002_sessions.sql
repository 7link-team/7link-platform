-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:18-24
-- Authentication-assurance columns: internal/modules/iam/sql/postgres/00019_authentication_assurance.sql:2-6.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_sessions (
    id_hash             CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id        BIGINT         NOT NULL,
    created_at          TIMESTAMPTZ(3) NOT NULL,
    auth_time           TIMESTAMPTZ(3) NOT NULL,
    acr                 INTEGER        NOT NULL DEFAULT 0,
    amr                 TEXT           COLLATE "C" NOT NULL DEFAULT '',
    last_seen_at        TIMESTAMPTZ(3) NOT NULL,
    expires_at          TIMESTAMPTZ(3) NOT NULL,
    absolute_expires_at TIMESTAMPTZ(3) NOT NULL,
    revoked_at          TIMESTAMPTZ(3) NULL,
    ip_address          VARCHAR(64)    COLLATE "C" NOT NULL DEFAULT '',
    user_agent          VARCHAR(512)   NOT NULL DEFAULT ''
);

CREATE INDEX {{.Prefix}}idx_iam_sessions_principal
    ON {{.Prefix}}iam_sessions (principal_id, revoked_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_sessions;
