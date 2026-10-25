-- +goose Up
-- TEXT already supports complete rubric receipt payloads in this dialect.
SELECT 1;

-- +goose Down
SELECT 1;
