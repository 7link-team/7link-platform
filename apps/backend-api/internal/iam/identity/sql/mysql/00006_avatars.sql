-- +goose Up
ALTER TABLE {{.Prefix}}iam_principals ADD COLUMN avatar VARCHAR(512) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE {{.Prefix}}iam_principals DROP COLUMN avatar;
