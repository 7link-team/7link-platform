-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_credentials_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_i
BEFORE INSERT ON {{.Prefix}}iam_credentials
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_credentials_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_credentials_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_credentials
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_credentials_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_sessions_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_i
BEFORE INSERT ON {{.Prefix}}iam_sessions
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_sessions_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_sessions_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_sessions
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_sessions_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_history_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_history_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_history
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_history_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_history_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_history_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_history
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_history_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_mfa_enroll_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_enrollments
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_mfa_enroll_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_mfa_enroll_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_enrollments
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_mfa_enroll_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_recovery_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_i
BEFORE INSERT ON {{.Prefix}}iam_recovery_codes
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_recovery_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_recovery_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_recovery_codes
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_recovery_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_mfa_chal_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_mfa_chal_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_mfa_chal_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_mfa_chal_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pk_users_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_users
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pk_users_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pk_users_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_users
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pk_users_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_passkeys_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkeys
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_passkeys_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_passkeys_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkeys
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_passkeys_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pk_chal_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pk_chal_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pk_chal_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pk_chal_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pw_tokens_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_recovery_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pw_tokens_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_pw_tokens_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_recovery_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_pw_tokens_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_email_tokens_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_email_verification_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_email_tokens_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_email_tokens_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_email_verification_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_email_tokens_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_stepup_pe_i() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_i
BEFORE INSERT ON {{.Prefix}}iam_stepup_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_stepup_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_stepup_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_stepup_challenges
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_stepup_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_outbox_pe_i() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF NEW.principal_id IS NULL OR NEW.principal_id <= 0 THEN
        RAISE EXCEPTION 'notification requires a principal';
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
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_i
BEFORE INSERT ON {{.Prefix}}iam_notification_outbox
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_outbox_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_outbox_pe_u() RETURNS trigger AS $$
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
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_notification_outbox
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_outbox_pe_u();

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}authn_outbox_pe_u ON {{.Prefix}}iam_notification_outbox;
DROP FUNCTION {{.Prefix}}authn_outbox_pe_u();
DROP TRIGGER {{.Prefix}}authn_outbox_pe_i ON {{.Prefix}}iam_notification_outbox;
DROP FUNCTION {{.Prefix}}authn_outbox_pe_i();
DROP TRIGGER {{.Prefix}}authn_stepup_pe_u ON {{.Prefix}}iam_stepup_challenges;
DROP FUNCTION {{.Prefix}}authn_stepup_pe_u();
DROP TRIGGER {{.Prefix}}authn_stepup_pe_i ON {{.Prefix}}iam_stepup_challenges;
DROP FUNCTION {{.Prefix}}authn_stepup_pe_i();
DROP TRIGGER {{.Prefix}}authn_email_tokens_pe_u ON {{.Prefix}}iam_email_verification_tokens;
DROP FUNCTION {{.Prefix}}authn_email_tokens_pe_u();
DROP TRIGGER {{.Prefix}}authn_email_tokens_pe_i ON {{.Prefix}}iam_email_verification_tokens;
DROP FUNCTION {{.Prefix}}authn_email_tokens_pe_i();
DROP TRIGGER {{.Prefix}}authn_pw_tokens_pe_u ON {{.Prefix}}iam_password_recovery_tokens;
DROP FUNCTION {{.Prefix}}authn_pw_tokens_pe_u();
DROP TRIGGER {{.Prefix}}authn_pw_tokens_pe_i ON {{.Prefix}}iam_password_recovery_tokens;
DROP FUNCTION {{.Prefix}}authn_pw_tokens_pe_i();
DROP TRIGGER {{.Prefix}}authn_pk_chal_pe_u ON {{.Prefix}}iam_passkey_challenges;
DROP FUNCTION {{.Prefix}}authn_pk_chal_pe_u();
DROP TRIGGER {{.Prefix}}authn_pk_chal_pe_i ON {{.Prefix}}iam_passkey_challenges;
DROP FUNCTION {{.Prefix}}authn_pk_chal_pe_i();
DROP TRIGGER {{.Prefix}}authn_passkeys_pe_u ON {{.Prefix}}iam_passkeys;
DROP FUNCTION {{.Prefix}}authn_passkeys_pe_u();
DROP TRIGGER {{.Prefix}}authn_passkeys_pe_i ON {{.Prefix}}iam_passkeys;
DROP FUNCTION {{.Prefix}}authn_passkeys_pe_i();
DROP TRIGGER {{.Prefix}}authn_pk_users_pe_u ON {{.Prefix}}iam_passkey_users;
DROP FUNCTION {{.Prefix}}authn_pk_users_pe_u();
DROP TRIGGER {{.Prefix}}authn_pk_users_pe_i ON {{.Prefix}}iam_passkey_users;
DROP FUNCTION {{.Prefix}}authn_pk_users_pe_i();
DROP TRIGGER {{.Prefix}}authn_mfa_chal_pe_u ON {{.Prefix}}iam_mfa_challenges;
DROP FUNCTION {{.Prefix}}authn_mfa_chal_pe_u();
DROP TRIGGER {{.Prefix}}authn_mfa_chal_pe_i ON {{.Prefix}}iam_mfa_challenges;
DROP FUNCTION {{.Prefix}}authn_mfa_chal_pe_i();
DROP TRIGGER {{.Prefix}}authn_recovery_pe_u ON {{.Prefix}}iam_recovery_codes;
DROP FUNCTION {{.Prefix}}authn_recovery_pe_u();
DROP TRIGGER {{.Prefix}}authn_recovery_pe_i ON {{.Prefix}}iam_recovery_codes;
DROP FUNCTION {{.Prefix}}authn_recovery_pe_i();
DROP TRIGGER {{.Prefix}}authn_mfa_enroll_pe_u ON {{.Prefix}}iam_mfa_enrollments;
DROP FUNCTION {{.Prefix}}authn_mfa_enroll_pe_u();
DROP TRIGGER {{.Prefix}}authn_mfa_enroll_pe_i ON {{.Prefix}}iam_mfa_enrollments;
DROP FUNCTION {{.Prefix}}authn_mfa_enroll_pe_i();
DROP TRIGGER {{.Prefix}}authn_history_pe_u ON {{.Prefix}}iam_password_history;
DROP FUNCTION {{.Prefix}}authn_history_pe_u();
DROP TRIGGER {{.Prefix}}authn_history_pe_i ON {{.Prefix}}iam_password_history;
DROP FUNCTION {{.Prefix}}authn_history_pe_i();
DROP TRIGGER {{.Prefix}}authn_sessions_pe_u ON {{.Prefix}}iam_sessions;
DROP FUNCTION {{.Prefix}}authn_sessions_pe_u();
DROP TRIGGER {{.Prefix}}authn_sessions_pe_i ON {{.Prefix}}iam_sessions;
DROP FUNCTION {{.Prefix}}authn_sessions_pe_i();
DROP TRIGGER {{.Prefix}}authn_credentials_pe_u ON {{.Prefix}}iam_credentials;
DROP FUNCTION {{.Prefix}}authn_credentials_pe_u();
DROP TRIGGER {{.Prefix}}authn_credentials_pe_i ON {{.Prefix}}iam_credentials;
DROP FUNCTION {{.Prefix}}authn_credentials_pe_i();
