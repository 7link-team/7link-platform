-- Source: internal/modules/iam/sql/postgres/00007_passkeys.sql:2-35
-- Width validation: internal/modules/authn/passkey.go:412-415,499-504.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_passkey_users (
    principal_id BIGINT         NOT NULL PRIMARY KEY,
    user_handle  VARCHAR(86)    COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_passkey_users_handle UNIQUE (user_handle)
);

CREATE TABLE {{.Prefix}}iam_passkeys (
    id_hash               CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id          BIGINT         NOT NULL,
    name                  VARCHAR(200)   NOT NULL,
    credential_ciphertext TEXT           COLLATE "C" NOT NULL,
    sign_count            BIGINT         NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ(3) NOT NULL,
    updated_at            TIMESTAMPTZ(3) NOT NULL,
    last_used_at          TIMESTAMPTZ(3) NULL
);

CREATE INDEX {{.Prefix}}idx_iam_passkeys_principal
    ON {{.Prefix}}iam_passkeys (principal_id, created_at);

CREATE TABLE {{.Prefix}}iam_passkey_challenges (
    id_hash      CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    kind         VARCHAR(16)    COLLATE "C" NOT NULL,
    principal_id BIGINT         NULL,
    return_url   TEXT           NOT NULL DEFAULT '',
    session_data TEXT           COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    consumed_at  TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}chk_iam_passkey_challenges_kind
        CHECK (kind IN ('registration', 'login'))
);

CREATE INDEX {{.Prefix}}idx_iam_passkey_challenges_expiry
    ON {{.Prefix}}iam_passkey_challenges (expires_at, consumed_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_passkey_challenges;
DROP TABLE {{.Prefix}}iam_passkeys;
DROP TABLE {{.Prefix}}iam_passkey_users;
