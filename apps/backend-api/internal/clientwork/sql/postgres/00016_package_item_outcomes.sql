-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN item_outcomes TEXT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN item_outcomes;
