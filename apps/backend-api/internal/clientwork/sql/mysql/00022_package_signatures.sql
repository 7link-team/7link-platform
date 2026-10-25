-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN signer_name VARCHAR(200) NULL;
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN signer_contact_id BIGINT NULL;
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN receipt_sha256 VARCHAR(64) NULL;
ALTER TABLE {{.Prefix}}clientwork_package_receipts ADD COLUMN receipt_json LONGTEXT NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN receipt_json;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN receipt_sha256;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN signer_contact_id;
ALTER TABLE {{.Prefix}}clientwork_package_receipts DROP COLUMN signer_name;
