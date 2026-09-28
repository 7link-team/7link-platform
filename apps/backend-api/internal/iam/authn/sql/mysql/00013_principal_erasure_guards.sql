-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_i
BEFORE INSERT ON {{.Prefix}}iam_credentials
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_credentials
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_i
BEFORE INSERT ON {{.Prefix}}iam_sessions
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_sessions
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_history_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_history
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_history_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_history
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_enrollments
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_enrollments
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_i
BEFORE INSERT ON {{.Prefix}}iam_recovery_codes
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_recovery_codes
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_users
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_users
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkeys
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkeys
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_recovery_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_recovery_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_email_verification_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_email_verification_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_i
BEFORE INSERT ON {{.Prefix}}iam_stepup_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_stepup_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_i
BEFORE INSERT ON {{.Prefix}}iam_notification_outbox
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NULL OR NEW.principal_id <= 0 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'notification requires a principal';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_notification_outbox
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}authn_outbox_pe_u;
DROP TRIGGER {{.Prefix}}authn_outbox_pe_i;
DROP TRIGGER {{.Prefix}}authn_stepup_pe_u;
DROP TRIGGER {{.Prefix}}authn_stepup_pe_i;
DROP TRIGGER {{.Prefix}}authn_email_tokens_pe_u;
DROP TRIGGER {{.Prefix}}authn_email_tokens_pe_i;
DROP TRIGGER {{.Prefix}}authn_pw_tokens_pe_u;
DROP TRIGGER {{.Prefix}}authn_pw_tokens_pe_i;
DROP TRIGGER {{.Prefix}}authn_pk_chal_pe_u;
DROP TRIGGER {{.Prefix}}authn_pk_chal_pe_i;
DROP TRIGGER {{.Prefix}}authn_passkeys_pe_u;
DROP TRIGGER {{.Prefix}}authn_passkeys_pe_i;
DROP TRIGGER {{.Prefix}}authn_pk_users_pe_u;
DROP TRIGGER {{.Prefix}}authn_pk_users_pe_i;
DROP TRIGGER {{.Prefix}}authn_mfa_chal_pe_u;
DROP TRIGGER {{.Prefix}}authn_mfa_chal_pe_i;
DROP TRIGGER {{.Prefix}}authn_recovery_pe_u;
DROP TRIGGER {{.Prefix}}authn_recovery_pe_i;
DROP TRIGGER {{.Prefix}}authn_mfa_enroll_pe_u;
DROP TRIGGER {{.Prefix}}authn_mfa_enroll_pe_i;
DROP TRIGGER {{.Prefix}}authn_history_pe_u;
DROP TRIGGER {{.Prefix}}authn_history_pe_i;
DROP TRIGGER {{.Prefix}}authn_sessions_pe_u;
DROP TRIGGER {{.Prefix}}authn_sessions_pe_i;
DROP TRIGGER {{.Prefix}}authn_credentials_pe_u;
DROP TRIGGER {{.Prefix}}authn_credentials_pe_i;
