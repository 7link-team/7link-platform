-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:41-53
-- Assurance fields: internal/modules/iam/sql/sqlite/00019_authentication_assurance.sql:11-14.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_refresh_tokens (
    id_hash      TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    family_id    TEXT     COLLATE BINARY NOT NULL,
    principal_id BIGINT   NOT NULL,
    client_id    BIGINT   NOT NULL,
    scope        TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    auth_time    DATETIME NOT NULL,
    acr          INTEGER  NOT NULL DEFAULT 0,
    amr          TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    used_at      DATETIME NULL,
    revoked_at   DATETIME NULL
);

CREATE INDEX {{.Prefix}}idx_iam_refresh_family
    ON {{.Prefix}}iam_refresh_tokens (family_id, revoked_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_refresh_tokens;
