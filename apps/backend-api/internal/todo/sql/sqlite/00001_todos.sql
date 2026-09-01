-- +goose Up
CREATE TABLE {{.Prefix}}todos (
    id           BIGINT   NOT NULL PRIMARY KEY,
    title        TEXT     NOT NULL,
    description  TEXT     NOT NULL DEFAULT '',
    status       TEXT     NOT NULL,
    priority     INTEGER  NOT NULL DEFAULT 3,
    due_at       DATETIME NULL,
    completed_at DATETIME NULL,
    version      INTEGER  NOT NULL DEFAULT 1,
    created_at   DATETIME NOT NULL,
    created_by   BIGINT   NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL,
    updated_by   BIGINT   NOT NULL DEFAULT 0,
    deleted_at   DATETIME NULL,
    deleted_by   BIGINT   NOT NULL DEFAULT 0
);

CREATE INDEX {{.Prefix}}idx_todos_live_created ON {{.Prefix}}todos (deleted_at, created_at, id);
CREATE INDEX {{.Prefix}}idx_todos_live_due     ON {{.Prefix}}todos (deleted_at, status, due_at);

-- +goose Down
DROP TABLE {{.Prefix}}todos;
