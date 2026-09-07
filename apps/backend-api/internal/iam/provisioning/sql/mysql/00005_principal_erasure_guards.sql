-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_resources
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = CONCAT('erased:', CAST(NEW.resource_id AS CHAR))) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_resources
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.resource_id IS NOT NULL AND NOT (NEW.resource_id <=> OLD.resource_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = CONCAT('erased:', CAST(NEW.resource_id AS CHAR))) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_target_resources
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_target_resources
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.resource_id IS NOT NULL AND NOT (NEW.resource_id <=> OLD.resource_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_u;
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_i;
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_u;
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_i;
