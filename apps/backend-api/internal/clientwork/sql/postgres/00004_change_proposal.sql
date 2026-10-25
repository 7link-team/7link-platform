-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_change_requests ADD COLUMN proposal TEXT NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_change_requests DROP COLUMN proposal;
