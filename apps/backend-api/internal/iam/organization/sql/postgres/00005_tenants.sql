-- Source: internal/modules/organization/sql/postgres/00005_tenants.sql:1-12.
-- Validation: internal/modules/organization/tenant.go:140-185,280-309.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenants (
    id         BIGINT         PRIMARY KEY,
    slug       VARCHAR(63)    COLLATE "C" NOT NULL,
    name       VARCHAR(128)   NOT NULL,
    status     VARCHAR(16)    COLLATE "C" NOT NULL,
    version    BIGINT         NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_tenants_slug UNIQUE (slug),
    CONSTRAINT {{.Prefix}}chk_iam_tenants_status
        CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT {{.Prefix}}chk_iam_tenants_version CHECK (version >= 1)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_tenants;
