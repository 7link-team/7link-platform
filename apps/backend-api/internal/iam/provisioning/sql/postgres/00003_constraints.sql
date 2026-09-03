-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/provisioning/sql/postgres/00001_scim.sql:3;
-- internal/modules/provisioning/sql/postgres/00002_scim_targets.sql:3.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_scim_directories
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_directories_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_scim_targets
    ADD CONSTRAINT {{.Prefix}}fk_iam_scim_targets_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_scim_targets
    DROP CONSTRAINT {{.Prefix}}fk_iam_scim_targets_tenant;
ALTER TABLE {{.Prefix}}iam_scim_directories
    DROP CONSTRAINT {{.Prefix}}fk_iam_scim_directories_tenant;
