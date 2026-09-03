-- Source: internal/modules/organization/sql/mysql/00005_tenants.sql:1-12
-- +goose Up
CREATE TABLE {{.Prefix}}iam_tenants (
    id BIGINT PRIMARY KEY,
    slug VARCHAR(63) NOT NULL UNIQUE,
    name VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL,
    version BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
-- +goose Down
DROP TABLE {{.Prefix}}iam_tenants;