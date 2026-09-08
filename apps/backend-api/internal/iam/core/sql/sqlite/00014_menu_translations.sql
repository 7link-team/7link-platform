-- +goose Up
ALTER TABLE {{.Prefix}}iam_menus ADD COLUMN translations TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE {{.Prefix}}iam_menus DROP COLUMN translations;
