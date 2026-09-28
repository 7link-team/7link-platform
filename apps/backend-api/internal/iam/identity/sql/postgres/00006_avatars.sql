-- +goose Up
ALTER TABLE {{.Prefix}}iam_principals ADD COLUMN avatar TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE {{.Prefix}}iam_principals DROP COLUMN avatar;
