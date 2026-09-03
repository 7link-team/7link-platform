-- Cross-module principal references, preserving the source's ON DELETE CASCADE semantics.
-- Sources: internal/modules/iam/sql/postgres/00003_local_identity.sql:13,19;
-- 00004_password_history.sql:4; 00005_mfa.sql:5,12,22; 00007_passkeys.sql:3,10;
-- 00008_password_recovery.sql:8; 00009_email_verification.sql:4; 00023_stepup_challenges.sql:4.
-- +goose Up
ALTER TABLE {{.Prefix}}iam_credentials
    ADD CONSTRAINT {{.Prefix}}fk_authn_credentials_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_sessions
    ADD CONSTRAINT {{.Prefix}}fk_authn_sessions_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_password_history
    ADD CONSTRAINT {{.Prefix}}fk_authn_password_history_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_mfa_enrollments
    ADD CONSTRAINT {{.Prefix}}fk_authn_mfa_enrollment_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_recovery_codes
    ADD CONSTRAINT {{.Prefix}}fk_authn_recovery_codes_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_mfa_challenges
    ADD CONSTRAINT {{.Prefix}}fk_authn_mfa_challenge_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_passkey_users
    ADD CONSTRAINT {{.Prefix}}fk_authn_passkey_users_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_passkeys
    ADD CONSTRAINT {{.Prefix}}fk_authn_passkeys_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_password_recovery_tokens
    ADD CONSTRAINT {{.Prefix}}fk_authn_password_recovery_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_email_verification_tokens
    ADD CONSTRAINT {{.Prefix}}fk_authn_email_verification_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;
ALTER TABLE {{.Prefix}}iam_stepup_challenges
    ADD CONSTRAINT {{.Prefix}}fk_authn_stepup_principal
    FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE {{.Prefix}}iam_stepup_challenges DROP CONSTRAINT {{.Prefix}}fk_authn_stepup_principal;
ALTER TABLE {{.Prefix}}iam_email_verification_tokens DROP CONSTRAINT {{.Prefix}}fk_authn_email_verification_principal;
ALTER TABLE {{.Prefix}}iam_password_recovery_tokens DROP CONSTRAINT {{.Prefix}}fk_authn_password_recovery_principal;
ALTER TABLE {{.Prefix}}iam_passkeys DROP CONSTRAINT {{.Prefix}}fk_authn_passkeys_principal;
ALTER TABLE {{.Prefix}}iam_passkey_users DROP CONSTRAINT {{.Prefix}}fk_authn_passkey_users_principal;
ALTER TABLE {{.Prefix}}iam_mfa_challenges DROP CONSTRAINT {{.Prefix}}fk_authn_mfa_challenge_principal;
ALTER TABLE {{.Prefix}}iam_recovery_codes DROP CONSTRAINT {{.Prefix}}fk_authn_recovery_codes_principal;
ALTER TABLE {{.Prefix}}iam_mfa_enrollments DROP CONSTRAINT {{.Prefix}}fk_authn_mfa_enrollment_principal;
ALTER TABLE {{.Prefix}}iam_password_history DROP CONSTRAINT {{.Prefix}}fk_authn_password_history_principal;
ALTER TABLE {{.Prefix}}iam_sessions DROP CONSTRAINT {{.Prefix}}fk_authn_sessions_principal;
ALTER TABLE {{.Prefix}}iam_credentials DROP CONSTRAINT {{.Prefix}}fk_authn_credentials_principal;
