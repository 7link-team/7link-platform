-- Cross-module constraints preserve the source's ON DELETE CASCADE semantics.
-- Sources: internal/modules/iam/sql/postgres/00003_local_identity.sql:27,41,78,80.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_oauth_codes
    ADD CONSTRAINT {{.Prefix}}fk_oauth_codes_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_oauth_consents
    ADD CONSTRAINT {{.Prefix}}fk_oauth_consents_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_oauth_consents
    ADD CONSTRAINT {{.Prefix}}fk_oauth_consents_tenant
    FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_refresh_tokens
    ADD CONSTRAINT {{.Prefix}}fk_oauth_refresh_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_refresh_tokens DROP CONSTRAINT {{.Prefix}}fk_oauth_refresh_principal;
ALTER TABLE {{.Prefix}}iam_oauth_consents DROP CONSTRAINT {{.Prefix}}fk_oauth_consents_tenant;
ALTER TABLE {{.Prefix}}iam_oauth_consents DROP CONSTRAINT {{.Prefix}}fk_oauth_consents_principal;
ALTER TABLE {{.Prefix}}iam_oauth_codes DROP CONSTRAINT {{.Prefix}}fk_oauth_codes_principal;
