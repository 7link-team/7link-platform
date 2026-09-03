-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/governance/sql/mysql/00001_access_requests.sql:24-25;
-- internal/modules/governance/sql/mysql/00002_access_reviews.sql:14-15.
-- MySQL DDL auto-commits; recovery drops constraints in reverse order before replay.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_access_requests
    ADD CONSTRAINT {{.Prefix}}fk_iam_access_requests_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_access_reviews
    ADD CONSTRAINT {{.Prefix}}fk_iam_access_reviews_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_access_reviews
    DROP FOREIGN KEY {{.Prefix}}fk_iam_access_reviews_tenant;
ALTER TABLE {{.Prefix}}iam_access_requests
    DROP FOREIGN KEY {{.Prefix}}fk_iam_access_requests_tenant;
