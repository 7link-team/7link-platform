-- Source: internal/modules/iam/sql/postgres/00005_mfa.sql:4-34
-- +goose Up
CREATE TABLE {{.Prefix}}iam_mfa_enrollments (
    principal_id      BIGINT         NOT NULL PRIMARY KEY,
    secret_ciphertext TEXT           COLLATE "C" NOT NULL,
    created_at        TIMESTAMPTZ(3) NOT NULL,
    expires_at        TIMESTAMPTZ(3) NOT NULL
);

CREATE TABLE {{.Prefix}}iam_recovery_codes (
    principal_id BIGINT         NOT NULL,
    code_hash    CHAR(64)       COLLATE "C" NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    used_at      TIMESTAMPTZ(3) NULL,
    PRIMARY KEY (principal_id, code_hash)
);

CREATE INDEX {{.Prefix}}idx_iam_recovery_codes_available
    ON {{.Prefix}}iam_recovery_codes (principal_id, used_at);

CREATE TABLE {{.Prefix}}iam_mfa_challenges (
    id_hash      CHAR(64)       COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT         NOT NULL,
    return_url   TEXT           NOT NULL,
    created_at   TIMESTAMPTZ(3) NOT NULL,
    expires_at   TIMESTAMPTZ(3) NOT NULL,
    consumed_at  TIMESTAMPTZ(3) NULL,
    attempts     INTEGER        NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_iam_mfa_challenges_principal
    ON {{.Prefix}}iam_mfa_challenges (principal_id, consumed_at, expires_at);

-- +goose Down
DROP TABLE {{.Prefix}}iam_mfa_challenges;
DROP TABLE {{.Prefix}}iam_recovery_codes;
DROP TABLE {{.Prefix}}iam_mfa_enrollments;
