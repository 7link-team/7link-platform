-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN item_outcomes LONGTEXT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN item_outcomes;
