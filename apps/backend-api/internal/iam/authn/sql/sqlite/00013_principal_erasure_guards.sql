-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_i
BEFORE INSERT ON {{.Prefix}}iam_credentials
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_credentials_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_credentials
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_i
BEFORE INSERT ON {{.Prefix}}iam_sessions
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_sessions_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_sessions
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_history_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_history
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_history_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_history
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_enrollments
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_enroll_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_enrollments
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_i
BEFORE INSERT ON {{.Prefix}}iam_recovery_codes
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_recovery_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_recovery_codes
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_mfa_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_mfa_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_mfa_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_users
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_users_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_users
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkeys
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_passkeys_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkeys
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_i
BEFORE INSERT ON {{.Prefix}}iam_passkey_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pk_chal_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_passkey_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_password_recovery_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_pw_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_password_recovery_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_i
BEFORE INSERT ON {{.Prefix}}iam_email_verification_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_email_tokens_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_email_verification_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_i
BEFORE INSERT ON {{.Prefix}}iam_stepup_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_stepup_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_stepup_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_i
BEFORE INSERT ON {{.Prefix}}iam_notification_outbox
BEGIN
    SELECT RAISE(ABORT, 'notification requires a principal') WHERE NEW.principal_id IS NULL OR NEW.principal_id <= 0;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_outbox_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_notification_outbox
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
