-- Cross-module tenant references preserve the source ON DELETE CASCADE behavior.
-- Sources: internal/modules/governance/sql/postgres/00001_access_requests.sql:22;
-- internal/modules/governance/sql/postgres/00002_access_reviews.sql:13.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_access_requests
    ADD CONSTRAINT {{.Prefix}}fk_iam_access_requests_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_access_reviews
    ADD CONSTRAINT {{.Prefix}}fk_iam_access_reviews_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_access_reviews
    DROP CONSTRAINT {{.Prefix}}fk_iam_access_reviews_tenant;
ALTER TABLE {{.Prefix}}iam_access_requests
    DROP CONSTRAINT {{.Prefix}}fk_iam_access_requests_tenant;
