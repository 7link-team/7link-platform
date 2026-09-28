-- +goose Up
ALTER TABLE {{.Prefix}}iam_tenants ADD COLUMN scope_kind VARCHAR(16) COLLATE "C" NOT NULL DEFAULT 'tenant'
    CHECK (scope_kind IN ('tenant', 'platform', 'system'));
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}organization_scope_kind_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.scope_kind IS DISTINCT FROM OLD.scope_kind THEN
        RAISE EXCEPTION 'administration scope kind is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}organization_scope_kind_immutable
BEFORE UPDATE OF scope_kind ON {{.Prefix}}iam_tenants FOR EACH ROW
EXECUTE FUNCTION {{.Prefix}}organization_scope_kind_immutable();

-- +goose Down
-- An older build must not reinterpret an administrative root as a tenant.
UPDATE {{.Prefix}}iam_tenants SET scope_kind = 'tenant' WHERE scope_kind <> 'tenant';
DROP TRIGGER {{.Prefix}}organization_scope_kind_immutable ON {{.Prefix}}iam_tenants;
DROP FUNCTION {{.Prefix}}organization_scope_kind_immutable();
ALTER TABLE {{.Prefix}}iam_tenants DROP COLUMN scope_kind;
