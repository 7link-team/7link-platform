-- +goose Up
CREATE TABLE {{.Prefix}}iam_personal_tokens (
    id BIGINT NOT NULL PRIMARY KEY,
    token_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL,
    principal_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    permissions TEXT NOT NULL,
    credential_version BIGINT NOT NULL,
    created_at DATETIME(3) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    revoked_at DATETIME(3) NULL,
    CONSTRAINT {{.Prefix}}uq_personal_token_hash UNIQUE (token_hash),
    CONSTRAINT {{.Prefix}}fk_personal_token_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_personal_token_tenant FOREIGN KEY (tenant_id) REFERENCES {{.Prefix}}iam_tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_personal_token_owner ON {{.Prefix}}iam_personal_tokens (principal_id, id);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_personal_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = NEW.principal_id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_personal_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_personal_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = NEW.principal_id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_personal_tokens;
