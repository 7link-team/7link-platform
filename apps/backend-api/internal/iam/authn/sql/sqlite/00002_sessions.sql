-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:27-39
-- Authentication-assurance columns: internal/modules/iam/sql/sqlite/00019_authentication_assurance.sql:2-6.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_sessions (
    id_hash             TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id        BIGINT   NOT NULL,
    created_at          DATETIME NOT NULL,
    auth_time           DATETIME NOT NULL,
    acr                 INTEGER  NOT NULL DEFAULT 0,
    amr                 TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    last_seen_at        DATETIME NOT NULL,
    expires_at          DATETIME NOT NULL,
    absolute_expires_at DATETIME NOT NULL,
    revoked_at          DATETIME NULL,
    ip_address          TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    user_agent          TEXT     NOT NULL DEFAULT ''
);

CREATE INDEX {{.Prefix}}idx_iam_sessions_principal
    ON {{.Prefix}}iam_sessions (principal_id, revoked_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_sessions;
