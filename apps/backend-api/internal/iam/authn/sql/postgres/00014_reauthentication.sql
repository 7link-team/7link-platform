-- +goose Up
CREATE TABLE {{.Prefix}}iam_reauth_challenges (
    id_hash VARCHAR(64) COLLATE "C" NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    session_hash VARCHAR(64) COLLATE "C" NOT NULL,
    credential_version BIGINT NOT NULL,
    purpose VARCHAR(64) NOT NULL,
    target VARCHAR(512) NOT NULL,
    method VARCHAR(32) NOT NULL,
    session_data TEXT NOT NULL,
    created_at TIMESTAMPTZ(3) NOT NULL,
    expires_at TIMESTAMPTZ(3) NOT NULL,
    verified_at TIMESTAMPTZ(3) NULL,
    consumed_at TIMESTAMPTZ(3) NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT {{.Prefix}}fk_reauth_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_reauth_principal ON {{.Prefix}}iam_reauth_challenges (principal_id, expires_at);

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_reauth_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_i
BEFORE INSERT ON {{.Prefix}}iam_reauth_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_reauth_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_reauth_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_reauth_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_reauth_pe_u();

-- +goose Down

DROP TABLE {{.Prefix}}iam_reauth_challenges;
DROP FUNCTION {{.Prefix}}authn_reauth_pe_i();
DROP FUNCTION {{.Prefix}}authn_reauth_pe_u();
