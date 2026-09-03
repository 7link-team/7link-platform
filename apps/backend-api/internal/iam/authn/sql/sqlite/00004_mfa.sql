-- Source: internal/modules/iam/sql/sqlite/00005_mfa.sql:4-37
-- +goose Up
CREATE TABLE {{.Prefix}}iam_mfa_enrollments (
    principal_id      BIGINT   NOT NULL PRIMARY KEY,
    secret_ciphertext TEXT     COLLATE BINARY NOT NULL,
    created_at        DATETIME NOT NULL,
    expires_at        DATETIME NOT NULL
);

CREATE TABLE {{.Prefix}}iam_recovery_codes (
    principal_id BIGINT   NOT NULL,
    code_hash    TEXT     COLLATE BINARY NOT NULL,
    created_at   DATETIME NOT NULL,
    used_at      DATETIME NULL,
    PRIMARY KEY (principal_id, code_hash)
);

CREATE INDEX {{.Prefix}}idx_iam_recovery_codes_available
    ON {{.Prefix}}iam_recovery_codes (principal_id, used_at);

CREATE TABLE {{.Prefix}}iam_mfa_challenges (
    id_hash      TEXT     COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT   NOT NULL,
    return_url   TEXT     NOT NULL,
    created_at   DATETIME NOT NULL,
    expires_at   DATETIME NOT NULL,
    consumed_at  DATETIME NULL,
    attempts     INTEGER  NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_iam_mfa_challenges_principal
    ON {{.Prefix}}iam_mfa_challenges (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_mfa_challenges;
DROP TABLE {{.Prefix}}iam_recovery_codes;
DROP TABLE {{.Prefix}}iam_mfa_enrollments;
