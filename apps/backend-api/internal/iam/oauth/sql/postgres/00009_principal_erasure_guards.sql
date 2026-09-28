-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_codes_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_codes_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_codes
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_codes_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_codes_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_codes_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_codes
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_codes_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_refresh_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_refresh_pe_i
BEFORE INSERT ON {{.Prefix}}iam_refresh_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_refresh_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_refresh_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_refresh_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_refresh_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_refresh_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_consents_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_consents_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_consents
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_consents_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_consents_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_consents_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_consents
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_consents_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_requests_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_requests_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_consent_requests
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_requests_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}oauth_requests_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}oauth_requests_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_consent_requests
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}oauth_requests_pe_u();

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}oauth_requests_pe_u ON {{.Prefix}}iam_oauth_consent_requests;
DROP FUNCTION {{.Prefix}}oauth_requests_pe_u();
DROP TRIGGER {{.Prefix}}oauth_requests_pe_i ON {{.Prefix}}iam_oauth_consent_requests;
DROP FUNCTION {{.Prefix}}oauth_requests_pe_i();
DROP TRIGGER {{.Prefix}}oauth_consents_pe_u ON {{.Prefix}}iam_oauth_consents;
DROP FUNCTION {{.Prefix}}oauth_consents_pe_u();
DROP TRIGGER {{.Prefix}}oauth_consents_pe_i ON {{.Prefix}}iam_oauth_consents;
DROP FUNCTION {{.Prefix}}oauth_consents_pe_i();
DROP TRIGGER {{.Prefix}}oauth_refresh_pe_u ON {{.Prefix}}iam_refresh_tokens;
DROP FUNCTION {{.Prefix}}oauth_refresh_pe_u();
DROP TRIGGER {{.Prefix}}oauth_refresh_pe_i ON {{.Prefix}}iam_refresh_tokens;
DROP FUNCTION {{.Prefix}}oauth_refresh_pe_i();
DROP TRIGGER {{.Prefix}}oauth_codes_pe_u ON {{.Prefix}}iam_oauth_codes;
DROP FUNCTION {{.Prefix}}oauth_codes_pe_u();
DROP TRIGGER {{.Prefix}}oauth_codes_pe_i ON {{.Prefix}}iam_oauth_codes;
DROP FUNCTION {{.Prefix}}oauth_codes_pe_i();
