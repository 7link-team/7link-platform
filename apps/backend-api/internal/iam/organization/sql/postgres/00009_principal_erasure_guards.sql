-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}organization_invites_pe_i() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF NEW.accepted_by IS NOT NULL THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.accepted_by FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.accepted_by FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.email = '' AND NEW.email_key = '') THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}organization_invites_pe_i
BEFORE INSERT ON {{.Prefix}}iam_invitations
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}organization_invites_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}organization_invites_pe_u() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF OLD.accepted_by IS NOT NULL AND NEW.accepted_by IS DISTINCT FROM OLD.accepted_by THEN
        RAISE EXCEPTION 'principal ownership is immutable';
    END IF;
    IF NEW.accepted_by IS NOT NULL THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.accepted_by FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.accepted_by FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.email = '' AND NEW.email_key = '') THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}organization_invites_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_invitations
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}organization_invites_pe_u();

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}organization_invites_pe_u ON {{.Prefix}}iam_invitations;
DROP FUNCTION {{.Prefix}}organization_invites_pe_u();
DROP TRIGGER {{.Prefix}}organization_invites_pe_i ON {{.Prefix}}iam_invitations;
DROP FUNCTION {{.Prefix}}organization_invites_pe_i();
