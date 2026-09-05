-- Holds one pending authorization request while its subject decides on the
-- consent screen. The row is the consent challenge: it is looked up by the
-- hash of the opaque value carried in the redirect, never by that value.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_consent_requests (
    challenge_hash CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    client_id      BIGINT         NOT NULL,
    principal_id   BIGINT         NOT NULL,
    redirect_uri   TEXT           COLLATE "C" NOT NULL,
    scope          TEXT           COLLATE "C" NOT NULL,
    state          TEXT           COLLATE "C" NOT NULL,
    code_challenge VARCHAR(128)   COLLATE "C" NOT NULL,
    nonce          VARCHAR(256)   COLLATE "C" NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ(3) NOT NULL,
    expires_at     TIMESTAMPTZ(3) NOT NULL,
    resolved_at    TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_consent_requests_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_consent_requests;
