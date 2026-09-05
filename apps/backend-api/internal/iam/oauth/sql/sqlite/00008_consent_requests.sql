-- Holds one pending authorization request while its subject decides on the
-- consent screen. The row is the consent challenge: it is looked up by the
-- hash of the opaque value carried in the redirect, never by that value.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_consent_requests (
    challenge_hash TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    client_id      BIGINT   NOT NULL,
    principal_id   BIGINT   NOT NULL,
    redirect_uri   TEXT     COLLATE BINARY NOT NULL,
    scope          TEXT     COLLATE BINARY NOT NULL,
    state          TEXT     COLLATE BINARY NOT NULL,
    code_challenge TEXT     COLLATE BINARY NOT NULL,
    nonce          TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    expires_at     DATETIME NOT NULL,
    resolved_at    DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_consent_requests_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_consent_requests;
