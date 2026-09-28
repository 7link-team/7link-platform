-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:15-25
-- Later source columns are folded into this fresh-database baseline.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_credentials (
    principal_id            BIGINT   NOT NULL PRIMARY KEY,
    password_hash           TEXT     COLLATE BINARY NOT NULL,
    totp_secret             TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    mfa_required            INTEGER  NOT NULL DEFAULT 0,
    failed_attempts         INTEGER  NOT NULL DEFAULT 0,
    locked_until            DATETIME NULL,
    password_changed_at     DATETIME NOT NULL,
    -- Source: internal/modules/iam/sql/sqlite/00005_mfa.sql:2.
    totp_last_used_step     BIGINT   NOT NULL DEFAULT 0,
    -- Source: internal/modules/iam/sql/sqlite/00008_password_recovery.sql:2-3.
    credential_version      BIGINT   NOT NULL DEFAULT 1,
    recovery_cooldown_until DATETIME NULL,
    updated_at              DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_credentials_mfa_required
        CHECK (mfa_required IN (0, 1))
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_credentials;
