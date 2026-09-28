-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_directories ADD COLUMN service_account_id BIGINT NULL;
ALTER TABLE {{.Prefix}}iam_scim_directories
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_directories_service_account
    FOREIGN KEY (service_account_id) REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE RESTRICT;
ALTER TABLE {{.Prefix}}iam_scim_directories
    ADD CONSTRAINT {{.Prefix}}chk_iam_scim_directories_service_account CHECK (service_account_id > 0);
ALTER TABLE {{.Prefix}}iam_scim_targets ADD COLUMN service_account_id BIGINT NULL;
ALTER TABLE {{.Prefix}}iam_scim_targets
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_targets_service_account
    FOREIGN KEY (service_account_id) REFERENCES {{.Prefix}}iam_service_accounts (principal_id) ON DELETE RESTRICT;
ALTER TABLE {{.Prefix}}iam_scim_targets
    ADD CONSTRAINT {{.Prefix}}chk_iam_scim_targets_service_account CHECK (service_account_id > 0);

-- +goose Down
ALTER TABLE {{.Prefix}}iam_scim_targets DROP CHECK {{.Prefix}}chk_iam_scim_targets_service_account;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP FOREIGN KEY {{.Prefix}}fk_iam_scim_targets_service_account;
ALTER TABLE {{.Prefix}}iam_scim_targets DROP COLUMN service_account_id;
ALTER TABLE {{.Prefix}}iam_scim_directories DROP CHECK {{.Prefix}}chk_iam_scim_directories_service_account;
ALTER TABLE {{.Prefix}}iam_scim_directories DROP FOREIGN KEY {{.Prefix}}fk_iam_scim_directories_service_account;
ALTER TABLE {{.Prefix}}iam_scim_directories DROP COLUMN service_account_id;
