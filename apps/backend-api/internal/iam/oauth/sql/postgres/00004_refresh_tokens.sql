-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:25-31
-- Assurance fields: internal/modules/iam/sql/postgres/00019_authentication_assurance.sql:11-14.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_refresh_tokens (
    id_hash      CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    family_id    VARCHAR(64)    COLLATE "C" NOT NULL,
    principal_id BIGINT         NOT NULL,
    client_id    BIGINT         NOT NULL,
    scope        TEXT           COLLATE "C" NOT NULL DEFAULT '',
    auth_time    TIMESTAMPTZ(3) NOT NULL,
    acr          INTEGER        NOT NULL DEFAULT 0,
    amr          TEXT           COLLATE "C" NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    used_at      TIMESTAMPTZ(3) NULL,
    revoked_at   TIMESTAMPTZ(3) NULL
);

CREATE INDEX {{.Prefix}}idx_iam_refresh_family
    ON {{.Prefix}}iam_refresh_tokens (family_id, revoked_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_refresh_tokens;
