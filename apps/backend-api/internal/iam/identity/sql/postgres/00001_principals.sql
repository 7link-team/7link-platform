-- Source: internal/modules/iam/sql/postgres/00003_local_identity.sql:3-11
-- Fresh-database baseline; the source's later principal columns are folded in below.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_principals (
    id                  BIGINT         NOT NULL PRIMARY KEY,
    -- Source validation: internal/modules/identity/service.go:118-125.
    login_name          VARCHAR(200)   COLLATE "C" NOT NULL,
    email               VARCHAR(320)   COLLATE "C" NOT NULL DEFAULT '',
    display_name        VARCHAR(128)   NOT NULL,
    status              VARCHAR(16)    COLLATE "C" NOT NULL,
    -- Source: internal/modules/iam/sql/postgres/00008_password_recovery.sql:4.
    email_verified      BOOLEAN        NOT NULL DEFAULT FALSE,
    -- Source: internal/modules/iam/sql/postgres/00014_self_registration.sql:2.
    activation_required BOOLEAN        NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ(3) NOT NULL,
    updated_at          TIMESTAMPTZ(3) NOT NULL,
    disabled_at         TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}uq_iam_principals_login UNIQUE (login_name),
    CONSTRAINT {{.Prefix}}chk_iam_principals_status
        CHECK (status IN ('active', 'disabled', 'locked'))
);

CREATE INDEX {{.Prefix}}idx_iam_principals_email
    ON {{.Prefix}}iam_principals (email);

-- +goose Down
DROP TABLE {{.Prefix}}iam_principals;
