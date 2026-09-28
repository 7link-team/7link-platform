-- Marks principals created by public self-registration. Only those may have a
-- pending credential replaced by a later public registration; directory,
-- invitation and administrator-created principals never are. Rows that existed
-- before this column default to FALSE, which refuses rather than allows.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_principals ADD COLUMN self_registered BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_principals DROP COLUMN self_registered;
