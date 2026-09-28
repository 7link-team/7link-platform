-- Source: internal/modules/iam/sql/mysql/00005_mfa.sql:4-37
-- MySQL DDL auto-commits; these three tables form one MFA aggregate and recover children first.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_mfa_enrollments (
    principal_id      BIGINT       NOT NULL PRIMARY KEY,
    secret_ciphertext TEXT         COLLATE utf8mb4_bin NOT NULL,
    created_at        DATETIME(3)  NOT NULL,
    expires_at        DATETIME(3)  NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_recovery_codes (
    principal_id BIGINT       NOT NULL,
    code_hash    CHAR(64)     COLLATE utf8mb4_bin NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    used_at      DATETIME(3)  NULL,
    PRIMARY KEY (principal_id, code_hash),
    INDEX {{.Prefix}}idx_iam_recovery_codes_available (principal_id, used_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE {{.Prefix}}iam_mfa_challenges (
    id_hash      CHAR(64)     COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id BIGINT       NOT NULL,
    return_url   TEXT         NOT NULL,
    created_at   DATETIME(3)  NOT NULL,
    expires_at   DATETIME(3)  NOT NULL,
    consumed_at  DATETIME(3)  NULL,
    attempts     INT          NOT NULL DEFAULT 0,
    INDEX {{.Prefix}}idx_iam_mfa_challenges_principal (principal_id, consumed_at, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_mfa_challenges;
DROP TABLE {{.Prefix}}iam_recovery_codes;
DROP TABLE {{.Prefix}}iam_mfa_enrollments;
