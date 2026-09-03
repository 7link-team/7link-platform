-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/provisioning/sql/mysql/00001_scim.sql:15-16;
-- internal/modules/provisioning/sql/mysql/00002_scim_targets.sql:15-16.
-- MySQL DDL auto-commits; recovery drops constraints in reverse order before replay.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_directories
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_directories_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_scim_targets
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_targets_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_scim_targets
    DROP FOREIGN KEY {{.Prefix}}fk_iam_scim_targets_tenant;
ALTER TABLE {{.Prefix}}iam_scim_directories
    DROP FOREIGN KEY {{.Prefix}}fk_iam_scim_directories_tenant;
