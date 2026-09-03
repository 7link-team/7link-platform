-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:12-17
-- Later source columns are folded into this fresh-database baseline.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_credentials (
    principal_id           BIGINT         NOT NULL PRIMARY KEY,
    password_hash          TEXT           COLLATE "C" NOT NULL,
    totp_secret            TEXT           COLLATE "C" NOT NULL DEFAULT '',
    mfa_required           BOOLEAN        NOT NULL DEFAULT FALSE,
    failed_attempts        INTEGER        NOT NULL DEFAULT 0,
    locked_until           TIMESTAMPTZ(3) NULL,
    password_changed_at    TIMESTAMPTZ(3) NOT NULL,
    -- Source: internal/modules/iam/sql/postgres/00005_mfa.sql:2.
    totp_last_used_step    BIGINT         NOT NULL DEFAULT 0,
    -- Source: internal/modules/iam/sql/postgres/00008_password_recovery.sql:2-3.
    credential_version     BIGINT         NOT NULL DEFAULT 1,
    recovery_cooldown_until TIMESTAMPTZ(3) NULL,
    updated_at             TIMESTAMPTZ(3) NOT NULL
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_credentials;
