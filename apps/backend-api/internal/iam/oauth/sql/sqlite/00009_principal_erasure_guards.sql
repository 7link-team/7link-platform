-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}oauth_codes_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_codes
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}oauth_codes_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_codes
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
CREATE TRIGGER {{.Prefix}}oauth_refresh_pe_i
BEFORE INSERT ON {{.Prefix}}iam_refresh_tokens
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}oauth_refresh_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_refresh_tokens
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
CREATE TRIGGER {{.Prefix}}oauth_consents_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_consents
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}oauth_consents_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_consents
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
CREATE TRIGGER {{.Prefix}}oauth_requests_pe_i
BEFORE INSERT ON {{.Prefix}}iam_oauth_consent_requests
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}oauth_requests_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_oauth_consent_requests
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
DROP TRIGGER {{.Prefix}}oauth_requests_pe_u;
DROP TRIGGER {{.Prefix}}oauth_requests_pe_i;
DROP TRIGGER {{.Prefix}}oauth_consents_pe_u;
DROP TRIGGER {{.Prefix}}oauth_consents_pe_i;
DROP TRIGGER {{.Prefix}}oauth_refresh_pe_u;
DROP TRIGGER {{.Prefix}}oauth_refresh_pe_i;
DROP TRIGGER {{.Prefix}}oauth_codes_pe_u;
DROP TRIGGER {{.Prefix}}oauth_codes_pe_i;
