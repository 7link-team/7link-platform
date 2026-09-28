-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_members_pe_i
BEFORE INSERT ON {{.Prefix}}iam_tenant_members
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.status = 'disabled' AND NEW.email = '' AND NEW.display_name = 'Anonymized account') THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_members_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_tenant_members
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
        IF erased IS NOT NULL AND NOT (NEW.status = 'disabled' AND NEW.email = '' AND NEW.display_name = 'Anonymized account') THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_admins_pe_i
BEFORE INSERT ON {{.Prefix}}iam_platform_administrators
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
CREATE TRIGGER {{.Prefix}}core_admins_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_platform_administrators
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
CREATE TRIGGER {{.Prefix}}core_grants_pe_i
BEFORE INSERT ON {{.Prefix}}iam_platform_grants
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
CREATE TRIGGER {{.Prefix}}core_grants_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_platform_grants
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
DROP TRIGGER {{.Prefix}}core_grants_pe_u;
DROP TRIGGER {{.Prefix}}core_grants_pe_i;
DROP TRIGGER {{.Prefix}}core_admins_pe_u;
DROP TRIGGER {{.Prefix}}core_admins_pe_i;
DROP TRIGGER {{.Prefix}}core_members_pe_u;
DROP TRIGGER {{.Prefix}}core_members_pe_i;
