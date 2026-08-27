-- +goose Up
CREATE TABLE widgets (
    id   BIGINT       NOT NULL PRIMARY KEY,
    name VARCHAR(255) NOT NULL
);

-- +goose Down
DROP TABLE widgets;
