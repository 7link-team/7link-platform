-- Source: internal/modules/iam/sql/postgres/00012_platform_administrators.sql:2-5.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_platform_administrators (
    principal_id BIGINT         NOT NULL PRIMARY KEY,
    created_at   TIMESTAMPTZ(3) NOT NULL
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_platform_administrators;
