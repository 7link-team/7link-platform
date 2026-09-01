-- +goose Up
CREATE TABLE {{.Prefix}}todos (
    id           BIGINT       NOT NULL PRIMARY KEY,
    title        VARCHAR(255) NOT NULL,
    description  TEXT         NOT NULL,
    status       VARCHAR(16)  NOT NULL,
    priority     SMALLINT     NOT NULL DEFAULT 3,
    due_at       DATETIME(3)  NULL,
    completed_at DATETIME(3)  NULL,
    version      INT          NOT NULL DEFAULT 1,
    created_at   DATETIME(3)  NOT NULL,
    created_by   BIGINT       NOT NULL DEFAULT 0,
    updated_at   DATETIME(3)  NOT NULL,
    updated_by   BIGINT       NOT NULL DEFAULT 0,
    deleted_at   DATETIME(3)  NULL,
    deleted_by   BIGINT       NOT NULL DEFAULT 0,
    INDEX {{.Prefix}}idx_todos_live_created (deleted_at, created_at, id),
    INDEX {{.Prefix}}idx_todos_live_due     (deleted_at, status, due_at)
);

-- +goose Down
DROP TABLE {{.Prefix}}todos;
