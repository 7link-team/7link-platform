-- Source: internal/modules/iam/sql/sqlite/00004_password_history.sql:2-12
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_history (
    id            BIGINT   NOT NULL PRIMARY KEY,
    principal_id  BIGINT   NOT NULL,
    password_hash TEXT     COLLATE BINARY NOT NULL,
    created_at    DATETIME NOT NULL
);

CREATE INDEX {{.Prefix}}idx_iam_password_history_principal
    ON {{.Prefix}}iam_password_history (principal_id, created_at DESC);

-- +goose Down
DROP TABLE {{.Prefix}}iam_password_history;
