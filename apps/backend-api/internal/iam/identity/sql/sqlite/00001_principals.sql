-- Source: internal/modules/iam/sql/sqlite/00003_local_identity.sql:3-13
-- Fresh-database baseline; the source's later principal columns are folded in below.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_principals (
    id                  BIGINT   NOT NULL PRIMARY KEY,
    -- Source validation: internal/modules/identity/service.go:118-125.
    login_name          TEXT     COLLATE BINARY NOT NULL,
    email               TEXT     COLLATE BINARY NOT NULL DEFAULT '',
    display_name        TEXT     NOT NULL,
    status              TEXT     COLLATE BINARY NOT NULL,
    -- Source: internal/modules/iam/sql/sqlite/00008_password_recovery.sql:4.
    email_verified      INTEGER  NOT NULL DEFAULT 0,
    -- Source: internal/modules/iam/sql/sqlite/00014_self_registration.sql:2.
    activation_required INTEGER  NOT NULL DEFAULT 0,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    disabled_at         DATETIME NULL,
    CONSTRAINT {{.Prefix}}uq_iam_principals_login UNIQUE (login_name),
    CONSTRAINT {{.Prefix}}chk_iam_principals_status
        CHECK (status IN ('active', 'disabled', 'locked')),
    CONSTRAINT {{.Prefix}}chk_iam_principals_email_verified
        CHECK (email_verified IN (0, 1)),
    CONSTRAINT {{.Prefix}}chk_iam_principals_activation_required
        CHECK (activation_required IN (0, 1))
);

CREATE INDEX {{.Prefix}}idx_iam_principals_email
    ON {{.Prefix}}iam_principals (email);

-- +goose Down
DROP TABLE {{.Prefix}}iam_principals;
