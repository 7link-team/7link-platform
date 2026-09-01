-- +goose Up
CREATE TABLE {{.Prefix}}dlocks (
    name       TEXT   NOT NULL PRIMARY KEY,
    owner      TEXT   NOT NULL,
    expires_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE {{.Prefix}}dlocks;
