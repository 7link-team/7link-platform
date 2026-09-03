-- Source: internal/modules/organization/sql/sqlite/00005_tenants.sql:1-12
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenants (
    id BIGINT NOT NULL PRIMARY KEY,
    slug TEXT NOT NULL COLLATE NOCASE UNIQUE,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'deleted')),
    version BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
-- +goose Down
DROP TABLE {{.Prefix}}iam_tenants;