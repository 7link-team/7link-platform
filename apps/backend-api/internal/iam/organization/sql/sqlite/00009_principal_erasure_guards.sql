-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}organization_invites_pe_i
BEFORE INSERT ON {{.Prefix}}iam_invitations
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.accepted_by IS NOT NULL AND NOT (NEW.email = '' AND NEW.email_key = '')
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.accepted_by AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}organization_invites_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_invitations
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.accepted_by IS NOT NULL AND NEW.accepted_by IS NOT OLD.accepted_by;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.accepted_by IS NOT NULL AND NOT (NEW.email = '' AND NEW.email_key = '')
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.accepted_by AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}organization_invites_pe_u;
DROP TRIGGER {{.Prefix}}organization_invites_pe_i;
