-- Source: internal/modules/organization/sql/postgres/00005_tenants.sql:1-12
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenants (
    id BIGINT PRIMARY KEY,
    slug VARCHAR(63) NOT NULL UNIQUE,
    name VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('active', 'suspended', 'deleted')),
    version BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
-- +goose Down
DROP TABLE {{.Prefix}}iam_tenants;