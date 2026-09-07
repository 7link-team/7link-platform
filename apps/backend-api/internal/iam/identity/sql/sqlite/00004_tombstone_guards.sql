-- Tombstones are irreversible. DDL rollback is not an operational restore.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_principal_tombstone_update
BEFORE UPDATE ON {{.Prefix}}iam_principals
WHEN EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = OLD.id AND anonymized_at IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_principal_tombstone_delete
BEFORE DELETE ON {{.Prefix}}iam_principals
WHEN EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = OLD.id AND anonymized_at IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_update
BEFORE UPDATE ON {{.Prefix}}iam_principal_lifecycles
WHEN OLD.anonymized_at IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_delete
BEFORE DELETE ON {{.Prefix}}iam_principal_lifecycles
WHEN OLD.anonymized_at IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized');
END;
-- +goose StatementEnd

-- +goose Down
-- Let the existing immutable-row trigger reject downgrade before any DDL runs.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_delete;
DROP TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_update;
DROP TRIGGER {{.Prefix}}trg_iam_principal_tombstone_delete;
DROP TRIGGER {{.Prefix}}trg_iam_principal_tombstone_update;
