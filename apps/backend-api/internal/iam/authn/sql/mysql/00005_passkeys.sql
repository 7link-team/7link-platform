-- Source: internal/modules/iam/sql/mysql/00007_passkeys.sql:2-38
-- Width validation: internal/modules/authn/passkey.go:412-415,499-504.
-- MySQL DDL auto-commits; these three tables form one passkey aggregate and recover children first.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_passkey_users (
    principal_id BIGINT       NOT NULL PRIMARY KEY,
    user_handle  VARCHAR(86)  COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_passkey_users_handle UNIQUE (user_handle)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_passkeys (
    id_hash               CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id          BIGINT        NOT NULL,
    name                  VARCHAR(200)  NOT NULL,
    credential_ciphertext TEXT          COLLATE utf8mb4_bin NOT NULL,
    sign_count            BIGINT UNSIGNED NOT NULL DEFAULT 0,
    created_at            DATETIME(3)   NOT NULL,
    updated_at            DATETIME(3)   NOT NULL,
    last_used_at          DATETIME(3)   NULL,
    INDEX {{.Prefix}}idx_iam_passkeys_principal (principal_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_passkey_challenges (
    id_hash      CHAR(64)      COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    kind         VARCHAR(16)   COLLATE utf8mb4_bin NOT NULL,
    principal_id BIGINT        NULL,
    return_url   TEXT          NOT NULL DEFAULT (''),
    session_data TEXT          COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)   NOT NULL,
    expires_at   DATETIME(3)   NOT NULL,
    consumed_at  DATETIME(3)   NULL,
    CONSTRAINT {{.Prefix}}chk_iam_passkey_challenges_kind
        CHECK (kind IN ('registration', 'login')),
    INDEX {{.Prefix}}idx_iam_passkey_challenges_expiry (expires_at, consumed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_passkey_challenges;
DROP TABLE {{.Prefix}}iam_passkeys;
DROP TABLE {{.Prefix}}iam_passkey_users;
