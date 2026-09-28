-- Tombstones are irreversible. DDL rollback is not an operational restore.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}protect_iam_principal_tombstone() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = OLD.id FOR UPDATE;
    IF erased IS NOT NULL THEN
        RAISE EXCEPTION 'principal is permanently anonymized';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER {{.Prefix}}trg_iam_principal_tombstone
BEFORE UPDATE OR DELETE ON {{.Prefix}}iam_principals
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}protect_iam_principal_tombstone();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}protect_iam_lifecycle_tombstone() RETURNS trigger AS $$
BEGIN
    IF OLD.anonymized_at IS NOT NULL THEN
        RAISE EXCEPTION 'principal is permanently anonymized';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone
BEFORE UPDATE OR DELETE ON {{.Prefix}}iam_principal_lifecycles
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}protect_iam_lifecycle_tombstone();

-- +goose Down
-- Let the existing immutable-row trigger reject downgrade before any DDL runs.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone ON {{.Prefix}}iam_principal_lifecycles;
DROP FUNCTION {{.Prefix}}protect_iam_lifecycle_tombstone();
DROP TRIGGER {{.Prefix}}trg_iam_principal_tombstone ON {{.Prefix}}iam_principals;
DROP FUNCTION {{.Prefix}}protect_iam_principal_tombstone();
