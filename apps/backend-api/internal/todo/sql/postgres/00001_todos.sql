-- +goose Up
CREATE TABLE {{.Prefix}}todos (
    id           BIGINT      NOT NULL PRIMARY KEY,
    title        VARCHAR(255) NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    status       VARCHAR(16) NOT NULL,
    priority     SMALLINT    NOT NULL DEFAULT 3,
    due_at       TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    version      INTEGER     NOT NULL DEFAULT 1,
    created_at   TIMESTAMPTZ NOT NULL,
    created_by   BIGINT      NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL,
    updated_by   BIGINT      NOT NULL DEFAULT 0,
    deleted_at   TIMESTAMPTZ NULL,
    deleted_by   BIGINT      NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_todos_live_created ON {{.Prefix}}todos (deleted_at, created_at, id);
CREATE INDEX {{.Prefix}}idx_todos_live_due     ON {{.Prefix}}todos (deleted_at, status, due_at);

-- +goose Down
DROP TABLE {{.Prefix}}todos;
