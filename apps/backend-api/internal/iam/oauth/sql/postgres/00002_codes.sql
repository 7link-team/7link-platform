-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:39-44
-- Assurance fields: internal/modules/iam/sql/postgres/00019_authentication_assurance.sql:7-10.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_codes (
    code_hash      CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    client_id      BIGINT         NOT NULL,
    principal_id   BIGINT         NOT NULL,
    redirect_uri   TEXT           COLLATE "C" NOT NULL,
    scope          TEXT           COLLATE "C" NOT NULL,
    code_challenge VARCHAR(128)   COLLATE "C" NOT NULL,
    nonce          VARCHAR(256)   COLLATE "C" NOT NULL DEFAULT '',
    auth_time      TIMESTAMPTZ(3) NOT NULL,
    acr            INTEGER        NOT NULL DEFAULT 0,
    amr            TEXT           COLLATE "C" NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ(3) NOT NULL,
    expires_at     TIMESTAMPTZ(3) NOT NULL,
    consumed_at    TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_codes;
