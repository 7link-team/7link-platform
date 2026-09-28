-- +goose Up
CREATE TABLE {{.Prefix}}iam_identity_link_transactions (
    id_hash VARCHAR(64) COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    provider_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    session_hash VARCHAR(64) COLLATE BINARY NOT NULL,
    credential_version BIGINT NOT NULL,
    purpose VARCHAR(32) NOT NULL,
    provider_fingerprint VARCHAR(64) COLLATE BINARY NOT NULL,
    challenge_hash VARCHAR(64) COLLATE BINARY NOT NULL,
    expected_subject VARCHAR(128) NOT NULL,
    code_verifier_ciphertext TEXT NOT NULL,
    callback_url TEXT NOT NULL,
    return_url TEXT NOT NULL,
    external_subject VARCHAR(128) NOT NULL,
    email VARCHAR(255) NOT NULL,
    display_name VARCHAR(128) NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    verified_at DATETIME NULL,
    consumed_at DATETIME NULL,
    CONSTRAINT {{.Prefix}}fk_link_tx_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_link_tx_provider FOREIGN KEY (provider_id) REFERENCES {{.Prefix}}iam_identity_providers(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_link_tx_principal ON {{.Prefix}}iam_identity_link_transactions (principal_id, expires_at);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}federation_link_tx_pe_i
BEFORE INSERT ON {{.Prefix}}iam_identity_link_transactions
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}federation_link_tx_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_identity_link_transactions
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_identity_link_transactions;
