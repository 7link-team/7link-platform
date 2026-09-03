-- Source: internal/modules/iam/sql/mysql/00003_local_identity.sql:3-8
-- MySQL DDL auto-commits; rerun recovery must drop this table before replaying a partial file.
-- Fresh-database baseline; the source's later principal columns are folded in below.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_principals (
    id                  BIGINT       NOT NULL PRIMARY KEY,
    -- Source validation: internal/modules/identity/service.go:118-125.
    login_name          VARCHAR(200) COLLATE utf8mb4_bin NOT NULL,
    email               VARCHAR(320) COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    display_name        VARCHAR(128) NOT NULL,
    status              VARCHAR(16)  COLLATE utf8mb4_bin NOT NULL,
    -- Source: internal/modules/iam/sql/mysql/00008_password_recovery.sql:5.
    email_verified      BOOLEAN      NOT NULL DEFAULT FALSE,
    -- Source: internal/modules/iam/sql/mysql/00014_self_registration.sql:2.
    activation_required BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at          DATETIME(3)  NOT NULL,
    updated_at          DATETIME(3)  NOT NULL,
    disabled_at         DATETIME(3)  NULL,
    CONSTRAINT {{.Prefix}}uq_iam_principals_login UNIQUE (login_name),
    CONSTRAINT {{.Prefix}}chk_iam_principals_status
        CHECK (status IN ('active', 'disabled', 'locked')),
    INDEX {{.Prefix}}idx_iam_principals_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE {{.Prefix}}iam_principals;
