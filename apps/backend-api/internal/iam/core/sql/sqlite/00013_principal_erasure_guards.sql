-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_members_pe_i
BEFORE INSERT ON {{.Prefix}}iam_tenant_members
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND NOT (NEW.status = 'disabled' AND NEW.email = '' AND NEW.display_name = 'Anonymized account')
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_members_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_tenant_members
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND NOT (NEW.status = 'disabled' AND NEW.email = '' AND NEW.display_name = 'Anonymized account')
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_admins_pe_i
BEFORE INSERT ON {{.Prefix}}iam_platform_administrators
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_admins_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_platform_administrators
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
CREATE TRIGGER {{.Prefix}}core_grants_pe_i
BEFORE INSERT ON {{.Prefix}}iam_platform_grants
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}core_grants_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_platform_grants
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
DROP TRIGGER {{.Prefix}}core_grants_pe_u;
DROP TRIGGER {{.Prefix}}core_grants_pe_i;
DROP TRIGGER {{.Prefix}}core_admins_pe_u;
DROP TRIGGER {{.Prefix}}core_admins_pe_i;
DROP TRIGGER {{.Prefix}}core_members_pe_u;
DROP TRIGGER {{.Prefix}}core_members_pe_i;
