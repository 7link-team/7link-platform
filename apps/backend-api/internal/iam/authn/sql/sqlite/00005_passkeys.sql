-- Source: internal/modules/iam/sql/sqlite/00007_passkeys.sql:2-37
-- Width validation: internal/modules/authn/passkey.go:412-415,499-504.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_passkey_users (
    principal_id BIGINT   NOT NULL PRIMARY KEY,
    user_handle  TEXT     COLLATE BINARY NOT NULL,
    created_at   DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_passkey_users_handle UNIQUE (user_handle)
);

CREATE TABLE {{.Prefix}}iam_passkeys (
    id_hash               TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id          BIGINT   NOT NULL,
    name                  TEXT     NOT NULL,
    credential_ciphertext TEXT     COLLATE BINARY NOT NULL,
    sign_count            BIGINT   NOT NULL DEFAULT 0,
    created_at            DATETIME NOT NULL,
    updated_at            DATETIME NOT NULL,
    last_used_at          DATETIME NULL
);

CREATE INDEX {{.Prefix}}idx_iam_passkeys_principal
    ON {{.Prefix}}iam_passkeys (principal_id, created_at);

CREATE TABLE {{.Prefix}}iam_passkey_challenges (
    id_hash      TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    kind         TEXT     COLLATE BINARY NOT NULL,
    principal_id BIGINT   NULL,
    return_url   TEXT     NOT NULL DEFAULT '',
    session_data TEXT     COLLATE BINARY NOT NULL,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL,
    CONSTRAINT {{.Prefix}}chk_iam_passkey_challenges_kind
        CHECK (kind IN ('registration', 'login'))
);

CREATE INDEX {{.Prefix}}idx_iam_passkey_challenges_expiry
    ON {{.Prefix}}iam_passkey_challenges (expires_at, consumed_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_passkey_challenges;
DROP TABLE {{.Prefix}}iam_passkeys;
DROP TABLE {{.Prefix}}iam_passkey_users;
