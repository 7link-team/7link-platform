-- +goose Up
CREATE TABLE {{.Prefix}}iam_personal_tokens (
    id BIGINT NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) COLLATE BINARY NOT NULL,
    principal_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    permissions TEXT NOT NULL,
    credential_version BIGINT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}uq_personal_token_hash UNIQUE (token_hash),
    CONSTRAINT {{.Prefix}}fk_personal_token_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_personal_token_tenant FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_personal_token_owner ON {{.Prefix}}iam_personal_tokens (principal_id, id);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_personal_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
    WHERE EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_personal_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable') WHERE NEW.principal_id != OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
    WHERE EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_personal_tokens;
