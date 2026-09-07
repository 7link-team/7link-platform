-- Tombstones are irreversible. DDL rollback is not an operational restore.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_principal_tombstone_update
BEFORE UPDATE ON {{.Prefix}}iam_principals
FOR EACH ROW
BEGIN
    DECLARE erased DATETIME(3) DEFAULT NULL;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = OLD.id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_principal_tombstone_delete
BEFORE DELETE ON {{.Prefix}}iam_principals
FOR EACH ROW
BEGIN
    DECLARE erased DATETIME(3) DEFAULT NULL;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
        WHERE principal_id = OLD.id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_update
BEFORE UPDATE ON {{.Prefix}}iam_principal_lifecycles
FOR EACH ROW
BEGIN
    IF OLD.anonymized_at IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_delete
BEFORE DELETE ON {{.Prefix}}iam_principal_lifecycles
FOR EACH ROW
BEGIN
    IF OLD.anonymized_at IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
-- Let the existing immutable-row trigger reject downgrade before any DDL runs.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_delete;
DROP TRIGGER {{.Prefix}}trg_iam_lifecycle_tombstone_update;
DROP TRIGGER {{.Prefix}}trg_iam_principal_tombstone_delete;
DROP TRIGGER {{.Prefix}}trg_iam_principal_tombstone_update;
