-- +goose Up
ALTER TABLE {{.Prefix}}agents_retirements ADD COLUMN precheck TEXT NOT NULL DEFAULT ('{}');
-- +goose Down
ALTER TABLE {{.Prefix}}agents_retirements DROP COLUMN precheck;
