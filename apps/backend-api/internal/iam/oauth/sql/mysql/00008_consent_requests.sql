-- Holds one pending authorization request while its subject decides on the
-- consent screen. The row is the consent challenge: it is looked up by the
-- hash of the opaque value carried in the redirect, never by that value.
-- MySQL DDL auto-commits; this file owns exactly one table for deterministic recovery.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_oauth_consent_requests (
    challenge_hash CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    client_id      BIGINT        NOT NULL,
    principal_id   BIGINT        NOT NULL,
    redirect_uri   TEXT          COLLATE utf8mb4_bin NOT NULL,
    scope          TEXT          COLLATE utf8mb4_bin NOT NULL,
    state          TEXT          COLLATE utf8mb4_bin NOT NULL,
    code_challenge VARCHAR(128)  COLLATE utf8mb4_bin NOT NULL,
    nonce          VARCHAR(256)  COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    created_at     DATETIME(3)   NOT NULL,
    expires_at     DATETIME(3)   NOT NULL,
    resolved_at    DATETIME(3)   NULL,
    CONSTRAINT {{.Prefix}}fk_oauth_consent_requests_client
        FOREIGN KEY (client_id) REFERENCES {{.Prefix}}iam_oauth_clients (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_oauth_consent_requests;
