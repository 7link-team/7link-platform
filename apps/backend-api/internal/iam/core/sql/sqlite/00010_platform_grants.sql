-- Source: internal/modules/iam/sql/sqlite/00024_platform_grants.sql:2-8.
-- Validation: internal/modules/iam/authorizer.go:88-90.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_platform_grants (
    principal_id    BIGINT   NOT NULL,
    permission_code TEXT     COLLATE BINARY NOT NULL,
    created_at      DATETIME NOT NULL,
    PRIMARY KEY (principal_id, permission_code)
);
CREATE INDEX {{.Prefix}}idx_iam_platform_grants_permission
    ON {{.Prefix}}iam_platform_grants (permission_code, principal_id);

-- +goose Down
DROP TABLE {{.Prefix}}iam_platform_grants;
