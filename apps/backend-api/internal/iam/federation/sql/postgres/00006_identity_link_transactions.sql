-- +goose Up
CREATE TABLE {{.Prefix}}iam_identity_link_transactions (
    id_hash VARCHAR(64) COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    provider_id BIGINT NOT NULL,
    tenant_id BIGINT NOT NULL,
    session_hash VARCHAR(64) COLLATE "C" NOT NULL,
    credential_version BIGINT NOT NULL,
    purpose VARCHAR(32) NOT NULL,
    provider_fingerprint VARCHAR(64) COLLATE "C" NOT NULL,
    challenge_hash VARCHAR(64) COLLATE "C" NOT NULL,
    expected_subject VARCHAR(128) NOT NULL,
    code_verifier_ciphertext TEXT NOT NULL,
    callback_url TEXT NOT NULL,
    return_url TEXT NOT NULL,
    external_subject VARCHAR(128) NOT NULL,
    email VARCHAR(255) NOT NULL,
    display_name VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    expires_at TIMESTAMPTZ(3) NOT NULL,
    verified_at TIMESTAMPTZ(3) NULL,
    consumed_at TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}fk_link_tx_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_link_tx_provider FOREIGN KEY (provider_id) REFERENCES {{.Prefix}}iam_identity_providers(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_link_tx_principal ON {{.Prefix}}iam_identity_link_transactions (principal_id, expires_at);

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}federation_link_tx_pe_i() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF NEW.principal_id IS NOT NULL THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}federation_link_tx_pe_i
BEFORE INSERT ON {{.Prefix}}iam_identity_link_transactions
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}federation_link_tx_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}federation_link_tx_pe_u() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF OLD.principal_id IS NOT NULL AND NEW.principal_id IS DISTINCT FROM OLD.principal_id THEN
        RAISE EXCEPTION 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}federation_link_tx_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_identity_link_transactions
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}federation_link_tx_pe_u();

-- +goose Down
DROP TABLE {{.Prefix}}iam_identity_link_transactions;
DROP FUNCTION {{.Prefix}}federation_link_tx_pe_i();
DROP FUNCTION {{.Prefix}}federation_link_tx_pe_u();
