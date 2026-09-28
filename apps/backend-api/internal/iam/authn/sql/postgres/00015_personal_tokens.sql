-- +goose Up
CREATE TABLE {{.Prefix}}iam_personal_tokens (
    id BIGINT NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL,
    principal_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    permissions TEXT NOT NULL,
    credential_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    expires_at TIMESTAMPTZ(3) NOT NULL,
    revoked_at TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}uq_personal_token_hash UNIQUE (token_hash),
    CONSTRAINT {{.Prefix}}fk_personal_token_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_personal_token_tenant FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_personal_token_owner ON {{.Prefix}}iam_personal_tokens (principal_id, id);

CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_personal_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_credentials_pe_i();

CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_personal_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_credentials_pe_u();

-- +goose Down
DROP TABLE {{.Prefix}}iam_personal_tokens;
