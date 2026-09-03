-- Cross-module principal references, preserving the source's ON DELETE CASCADE semantics.
-- Sources: internal/modules/iam/sql/sqlite/00003_local_identity.sql:24,37;
-- 00004_password_history.sql:7; 00005_mfa.sql:9,18,30; 00007_passkeys.sql:6,18;
-- 00008_password_recovery.sql:12; 00009_email_verification.sql:9; 00023_stepup_challenges.sql:10.
-- SQLite cannot add or drop foreign keys in place, so this milestone rebuilds each child table.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_credentials RENAME TO {{.Prefix}}iam_credentials_old;
CREATE TABLE {{.Prefix}}iam_credentials (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    password_hash TEXT COLLATE BINARY NOT NULL,
    totp_secret TEXT COLLATE BINARY NOT NULL DEFAULT '',
    mfa_required INTEGER NOT NULL DEFAULT 0,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME NULL,
    password_changed_at DATETIME NOT NULL,
    totp_last_used_step BIGINT NOT NULL DEFAULT 0,
    credential_version BIGINT NOT NULL DEFAULT 1,
    recovery_cooldown_until DATETIME NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_credentials_mfa_required CHECK (mfa_required IN (0, 1)),
    CONSTRAINT {{.Prefix}}fk_authn_credentials_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_credentials
SELECT * FROM {{.Prefix}}iam_credentials_old;
DROP TABLE {{.Prefix}}iam_credentials_old;

ALTER TABLE {{.Prefix}}iam_sessions RENAME TO {{.Prefix}}iam_sessions_old;
CREATE TABLE {{.Prefix}}iam_sessions (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    last_seen_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    absolute_expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    ip_address TEXT COLLATE BINARY NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    CONSTRAINT {{.Prefix}}fk_authn_sessions_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_sessions SELECT * FROM {{.Prefix}}iam_sessions_old;
DROP TABLE {{.Prefix}}iam_sessions_old;
CREATE INDEX {{.Prefix}}idx_iam_sessions_principal
    ON {{.Prefix}}iam_sessions (principal_id, revoked_at, expires_at);

ALTER TABLE {{.Prefix}}iam_password_history RENAME TO {{.Prefix}}iam_password_history_old;
CREATE TABLE {{.Prefix}}iam_password_history (
    id BIGINT NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    password_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}fk_authn_password_history_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_password_history SELECT * FROM {{.Prefix}}iam_password_history_old;
DROP TABLE {{.Prefix}}iam_password_history_old;
CREATE INDEX {{.Prefix}}idx_iam_password_history_principal
    ON {{.Prefix}}iam_password_history (principal_id, created_at DESC);

ALTER TABLE {{.Prefix}}iam_mfa_enrollments RENAME TO {{.Prefix}}iam_mfa_enrollments_old;
CREATE TABLE {{.Prefix}}iam_mfa_enrollments (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    secret_ciphertext TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}fk_authn_mfa_enrollment_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_mfa_enrollments SELECT * FROM {{.Prefix}}iam_mfa_enrollments_old;
DROP TABLE {{.Prefix}}iam_mfa_enrollments_old;

ALTER TABLE {{.Prefix}}iam_recovery_codes RENAME TO {{.Prefix}}iam_recovery_codes_old;
CREATE TABLE {{.Prefix}}iam_recovery_codes (
    principal_id BIGINT NOT NULL,
    code_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    PRIMARY KEY (principal_id, code_hash),
    CONSTRAINT {{.Prefix}}fk_authn_recovery_codes_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_recovery_codes SELECT * FROM {{.Prefix}}iam_recovery_codes_old;
DROP TABLE {{.Prefix}}iam_recovery_codes_old;
CREATE INDEX {{.Prefix}}idx_iam_recovery_codes_available
    ON {{.Prefix}}iam_recovery_codes (principal_id, used_at);

ALTER TABLE {{.Prefix}}iam_mfa_challenges RENAME TO {{.Prefix}}iam_mfa_challenges_old;
CREATE TABLE {{.Prefix}}iam_mfa_challenges (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    return_url TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT {{.Prefix}}fk_authn_mfa_challenge_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_mfa_challenges SELECT * FROM {{.Prefix}}iam_mfa_challenges_old;
DROP TABLE {{.Prefix}}iam_mfa_challenges_old;
CREATE INDEX {{.Prefix}}idx_iam_mfa_challenges_principal
    ON {{.Prefix}}iam_mfa_challenges (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_passkey_users RENAME TO {{.Prefix}}iam_passkey_users_old;
CREATE TABLE {{.Prefix}}iam_passkey_users (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    user_handle TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_passkey_users_handle UNIQUE (user_handle),
    CONSTRAINT {{.Prefix}}fk_authn_passkey_users_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_passkey_users SELECT * FROM {{.Prefix}}iam_passkey_users_old;
DROP TABLE {{.Prefix}}iam_passkey_users_old;

ALTER TABLE {{.Prefix}}iam_passkeys RENAME TO {{.Prefix}}iam_passkeys_old;
CREATE TABLE {{.Prefix}}iam_passkeys (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    credential_ciphertext TEXT COLLATE BINARY NOT NULL,
    sign_count BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    last_used_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_authn_passkeys_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_passkeys SELECT * FROM {{.Prefix}}iam_passkeys_old;
DROP TABLE {{.Prefix}}iam_passkeys_old;
CREATE INDEX {{.Prefix}}idx_iam_passkeys_principal
    ON {{.Prefix}}iam_passkeys (principal_id, created_at);

ALTER TABLE {{.Prefix}}iam_password_recovery_tokens RENAME TO {{.Prefix}}iam_password_recovery_tokens_old;
CREATE TABLE {{.Prefix}}iam_password_recovery_tokens (
    token_hmac TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_authn_password_recovery_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_password_recovery_tokens SELECT * FROM {{.Prefix}}iam_password_recovery_tokens_old;
DROP TABLE {{.Prefix}}iam_password_recovery_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_password_recovery_principal
    ON {{.Prefix}}iam_password_recovery_tokens (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_email_verification_tokens RENAME TO {{.Prefix}}iam_email_verification_tokens_old;
CREATE TABLE {{.Prefix}}iam_email_verification_tokens (
    token_hmac TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    email TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    code TEXT COLLATE BINARY NOT NULL DEFAULT '',
    CONSTRAINT {{.Prefix}}fk_authn_email_verification_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_email_verification_tokens SELECT * FROM {{.Prefix}}iam_email_verification_tokens_old;
DROP TABLE {{.Prefix}}iam_email_verification_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_email_verification_principal
    ON {{.Prefix}}iam_email_verification_tokens (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_stepup_challenges RENAME TO {{.Prefix}}iam_stepup_challenges_old;
CREATE TABLE {{.Prefix}}iam_stepup_challenges (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    session_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT {{.Prefix}}fk_authn_stepup_principal
        FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE
);
INSERT INTO {{.Prefix}}iam_stepup_challenges SELECT * FROM {{.Prefix}}iam_stepup_challenges_old;
DROP TABLE {{.Prefix}}iam_stepup_challenges_old;
CREATE INDEX {{.Prefix}}idx_iam_stepup_challenges_principal
    ON {{.Prefix}}iam_stepup_challenges (principal_id, consumed_at, expires_at);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_stepup_challenges RENAME TO {{.Prefix}}iam_stepup_challenges_old;
CREATE TABLE {{.Prefix}}iam_stepup_challenges (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    session_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    attempts INTEGER NOT NULL DEFAULT 0
);
INSERT INTO {{.Prefix}}iam_stepup_challenges SELECT * FROM {{.Prefix}}iam_stepup_challenges_old;
DROP TABLE {{.Prefix}}iam_stepup_challenges_old;
CREATE INDEX {{.Prefix}}idx_iam_stepup_challenges_principal
    ON {{.Prefix}}iam_stepup_challenges (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_email_verification_tokens RENAME TO {{.Prefix}}iam_email_verification_tokens_old;
CREATE TABLE {{.Prefix}}iam_email_verification_tokens (
    token_hmac TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    email TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    code TEXT COLLATE BINARY NOT NULL DEFAULT ''
);
INSERT INTO {{.Prefix}}iam_email_verification_tokens SELECT * FROM {{.Prefix}}iam_email_verification_tokens_old;
DROP TABLE {{.Prefix}}iam_email_verification_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_email_verification_principal
    ON {{.Prefix}}iam_email_verification_tokens (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_password_recovery_tokens RENAME TO {{.Prefix}}iam_password_recovery_tokens_old;
CREATE TABLE {{.Prefix}}iam_password_recovery_tokens (
    token_hmac TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL
);
INSERT INTO {{.Prefix}}iam_password_recovery_tokens SELECT * FROM {{.Prefix}}iam_password_recovery_tokens_old;
DROP TABLE {{.Prefix}}iam_password_recovery_tokens_old;
CREATE INDEX {{.Prefix}}idx_iam_password_recovery_principal
    ON {{.Prefix}}iam_password_recovery_tokens (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_passkeys RENAME TO {{.Prefix}}iam_passkeys_old;
CREATE TABLE {{.Prefix}}iam_passkeys (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    credential_ciphertext TEXT COLLATE BINARY NOT NULL,
    sign_count BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    last_used_at DATETIME NULL
);
INSERT INTO {{.Prefix}}iam_passkeys SELECT * FROM {{.Prefix}}iam_passkeys_old;
DROP TABLE {{.Prefix}}iam_passkeys_old;
CREATE INDEX {{.Prefix}}idx_iam_passkeys_principal
    ON {{.Prefix}}iam_passkeys (principal_id, created_at);

ALTER TABLE {{.Prefix}}iam_passkey_users RENAME TO {{.Prefix}}iam_passkey_users_old;
CREATE TABLE {{.Prefix}}iam_passkey_users (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    user_handle TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_passkey_users_handle UNIQUE (user_handle)
);
INSERT INTO {{.Prefix}}iam_passkey_users SELECT * FROM {{.Prefix}}iam_passkey_users_old;
DROP TABLE {{.Prefix}}iam_passkey_users_old;

ALTER TABLE {{.Prefix}}iam_mfa_challenges RENAME TO {{.Prefix}}iam_mfa_challenges_old;
CREATE TABLE {{.Prefix}}iam_mfa_challenges (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    return_url TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    consumed_at DATETIME NULL,
    attempts INTEGER NOT NULL DEFAULT 0
);
INSERT INTO {{.Prefix}}iam_mfa_challenges SELECT * FROM {{.Prefix}}iam_mfa_challenges_old;
DROP TABLE {{.Prefix}}iam_mfa_challenges_old;
CREATE INDEX {{.Prefix}}idx_iam_mfa_challenges_principal
    ON {{.Prefix}}iam_mfa_challenges (principal_id, consumed_at, expires_at);

ALTER TABLE {{.Prefix}}iam_recovery_codes RENAME TO {{.Prefix}}iam_recovery_codes_old;
CREATE TABLE {{.Prefix}}iam_recovery_codes (
    principal_id BIGINT NOT NULL,
    code_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    used_at DATETIME NULL,
    PRIMARY KEY (principal_id, code_hash)
);
INSERT INTO {{.Prefix}}iam_recovery_codes SELECT * FROM {{.Prefix}}iam_recovery_codes_old;
DROP TABLE {{.Prefix}}iam_recovery_codes_old;
CREATE INDEX {{.Prefix}}idx_iam_recovery_codes_available
    ON {{.Prefix}}iam_recovery_codes (principal_id, used_at);

ALTER TABLE {{.Prefix}}iam_mfa_enrollments RENAME TO {{.Prefix}}iam_mfa_enrollments_old;
CREATE TABLE {{.Prefix}}iam_mfa_enrollments (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    secret_ciphertext TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
INSERT INTO {{.Prefix}}iam_mfa_enrollments SELECT * FROM {{.Prefix}}iam_mfa_enrollments_old;
DROP TABLE {{.Prefix}}iam_mfa_enrollments_old;

ALTER TABLE {{.Prefix}}iam_password_history RENAME TO {{.Prefix}}iam_password_history_old;
CREATE TABLE {{.Prefix}}iam_password_history (
    id BIGINT NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    password_hash TEXT COLLATE BINARY NOT NULL,
    created_at DATETIME NOT NULL
);
INSERT INTO {{.Prefix}}iam_password_history SELECT * FROM {{.Prefix}}iam_password_history_old;
DROP TABLE {{.Prefix}}iam_password_history_old;
CREATE INDEX {{.Prefix}}idx_iam_password_history_principal
    ON {{.Prefix}}iam_password_history (principal_id, created_at DESC);

ALTER TABLE {{.Prefix}}iam_sessions RENAME TO {{.Prefix}}iam_sessions_old;
CREATE TABLE {{.Prefix}}iam_sessions (
    id_hash TEXT COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    auth_time DATETIME NOT NULL,
    acr INTEGER NOT NULL DEFAULT 0,
    amr TEXT COLLATE BINARY NOT NULL DEFAULT '',
    last_seen_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    absolute_expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    ip_address TEXT COLLATE BINARY NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);
INSERT INTO {{.Prefix}}iam_sessions SELECT * FROM {{.Prefix}}iam_sessions_old;
DROP TABLE {{.Prefix}}iam_sessions_old;
CREATE INDEX {{.Prefix}}idx_iam_sessions_principal
    ON {{.Prefix}}iam_sessions (principal_id, revoked_at, expires_at);

ALTER TABLE {{.Prefix}}iam_credentials RENAME TO {{.Prefix}}iam_credentials_old;
CREATE TABLE {{.Prefix}}iam_credentials (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    password_hash TEXT COLLATE BINARY NOT NULL,
    totp_secret TEXT COLLATE BINARY NOT NULL DEFAULT '',
    mfa_required INTEGER NOT NULL DEFAULT 0,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until DATETIME NULL,
    password_changed_at DATETIME NOT NULL,
    totp_last_used_step BIGINT NOT NULL DEFAULT 0,
    credential_version BIGINT NOT NULL DEFAULT 1,
    recovery_cooldown_until DATETIME NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}chk_iam_credentials_mfa_required CHECK (mfa_required IN (0, 1))
);
INSERT INTO {{.Prefix}}iam_credentials SELECT * FROM {{.Prefix}}iam_credentials_old;
DROP TABLE {{.Prefix}}iam_credentials_old;
