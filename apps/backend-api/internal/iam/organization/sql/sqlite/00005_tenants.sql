-- Source: internal/modules/organization/sql/sqlite/00005_tenants.sql:1-12.
-- Validation: internal/modules/organization/tenant.go:140-185,280-309.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenants (
    id         BIGINT   NOT NULL PRIMARY KEY,
    slug       TEXT     COLLATE BINARY NOT NULL,
    name       TEXT     NOT NULL,
    status     TEXT     COLLATE BINARY NOT NULL,
    version    BIGINT   NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}uq_iam_tenants_slug UNIQUE (slug),
    CONSTRAINT {{.Prefix}}chk_iam_tenants_status
        CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT {{.Prefix}}chk_iam_tenants_version CHECK (version >= 1)
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_tenants;
