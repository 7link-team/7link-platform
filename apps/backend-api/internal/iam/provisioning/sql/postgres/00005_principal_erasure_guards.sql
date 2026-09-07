-- Serialize operational profile/security writes with principal erasure.
-- Parent locks and current lifecycle reads fence pre-erasure snapshots.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}provisioning_resources_pe_i() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = 'erased:' || CAST(NEW.resource_id AS TEXT)) THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_resources
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}provisioning_resources_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}provisioning_resources_pe_u() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF OLD.resource_id IS NOT NULL AND NEW.resource_id IS DISTINCT FROM OLD.resource_id THEN
        RAISE EXCEPTION 'principal ownership is immutable';
    END IF;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL AND NEW.external_key = 'erased:' || CAST(NEW.resource_id AS TEXT)) THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}provisioning_resources_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_resources
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}provisioning_resources_pe_u();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}provisioning_targets_pe_i() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL) THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_i
BEFORE INSERT ON {{.Prefix}}iam_scim_target_resources
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}provisioning_targets_pe_i();

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}provisioning_targets_pe_u() RETURNS trigger AS $$
DECLARE erased TIMESTAMPTZ(3);
BEGIN
    IF OLD.resource_id IS NOT NULL AND NEW.resource_id IS DISTINCT FROM OLD.resource_id THEN
        RAISE EXCEPTION 'principal ownership is immutable';
    END IF;
    IF NEW.resource_id IS NOT NULL AND NEW.resource_type = 'User' THEN
        PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.resource_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.resource_id FOR UPDATE;
        IF erased IS NOT NULL AND NOT (NEW.external_id = '' AND NEW.deleted_at IS NOT NULL) THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}provisioning_targets_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_scim_target_resources
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}provisioning_targets_pe_u();

-- +goose Down
-- Refuse to remove protection once any irreversible erasure has committed.
UPDATE {{.Prefix}}iam_principal_lifecycles SET version = version WHERE anonymized_at IS NOT NULL;
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_u ON {{.Prefix}}iam_scim_target_resources;
DROP FUNCTION {{.Prefix}}provisioning_targets_pe_u();
DROP TRIGGER {{.Prefix}}provisioning_targets_pe_i ON {{.Prefix}}iam_scim_target_resources;
DROP FUNCTION {{.Prefix}}provisioning_targets_pe_i();
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_u ON {{.Prefix}}iam_scim_resources;
DROP FUNCTION {{.Prefix}}provisioning_resources_pe_u();
DROP TRIGGER {{.Prefix}}provisioning_resources_pe_i ON {{.Prefix}}iam_scim_resources;
DROP FUNCTION {{.Prefix}}provisioning_resources_pe_i();
