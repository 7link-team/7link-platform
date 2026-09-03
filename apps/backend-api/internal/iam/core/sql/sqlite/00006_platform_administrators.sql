-- Source: internal/modules/iam/sql/sqlite/00012_platform_administrators.sql:2-5.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_platform_administrators (
    principal_id BIGINT   NOT NULL PRIMARY KEY,
    created_at   DATETIME NOT NULL
);

-- +goose Down
DROP TABLE {{.Prefix}}iam_platform_administrators;
