-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_resources
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = 'erased:' || CAST(NEW.resource_id AS TEXT))
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_resources
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.resource_id IS NOT NULL AND NEW.resource_id IS NOT OLD.resource_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = 'erased:' || CAST(NEW.resource_id AS TEXT))
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_target_resources
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL)
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_target_resources
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.resource_id IS NOT NULL AND NEW.resource_id IS NOT OLD.resource_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL)
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_u;
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_i;
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_u;
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_i;
