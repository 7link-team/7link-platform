-- Source: internal/modules/iam/sql/postgres/00004_password_history.sql:2-11
-- +goose Up
CREATE TABLE {{.Prefix}}iam_password_history (
    id            BIGINT         NOT NULL PRIMARY KEY,
    principal_id  BIGINT         NOT NULL,
    password_hash TEXT           COLLATE "C" NOT NULL,
    created_at    TIMESTAMPTZ(3) NOT NULL
);

CREATE INDEX {{.Prefix}}idx_iam_password_history_principal
    ON {{.Prefix}}iam_password_history (principal_id, created_at DESC);

-- +goose Down
DROP TABLE {{.Prefix}}iam_password_history;
