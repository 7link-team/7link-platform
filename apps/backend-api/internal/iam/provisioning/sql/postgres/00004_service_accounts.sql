-- Existing integrations remain unbound until an operator selects their actor.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_directories
    ADD COLUMN service_account_id BIGINT NULL
    CONSTRAINT {{.Prefix}}fk_iam_scim_directories_service_account
    REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE RESTRICT
    CHECK (service_account_id > 0);
ALTER TABLE {{.Prefix}}iam_scim_targets
    ADD COLUMN service_account_id BIGINT NULL
    CONSTRAINT {{.Prefix}}fk_iam_scim_targets_service_account
    REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE RESTRICT
    CHECK (service_account_id > 0);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN service_account_id;
ALTER TABLE {{.Prefix}}iam_scim_directories DROP COLUMN service_account_id;
