-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:70-83
-- Assurance fields: internal/modules/iam/sql/sqlite/00019_authentication_assurance.sql:7-10.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_codes (
    code_hash      TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    client_id      BIGINT   NOT NULL,
    principal_id   BIGINT   NOT NULL,
    redirect_uri   TEXT     COLLATE BINARY NOT NULL,
    scope          TEXT     COLLATE BINARY NOT NULL,
    code_challenge TEXT     COLLATE BINARY NOT NULL,
    nonce          TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    auth_time      DATETIME NOT NULL,
    acr            INTEGER  NOT NULL DEFAULT 0,
    amr            TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    expires_at     DATETIME NOT NULL,
    consumed_at    DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_codes_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_codes;
