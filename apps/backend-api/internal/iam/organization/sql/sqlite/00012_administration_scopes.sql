-- +goose Up
ALTER TABLE {{.Prefix}}iam_tenants ADD COLUMN scope_kind VARCHAR(16) COLLATE BINARY NOT NULL DEFAULT 'tenant'
    CHECK (scope_kind IN ('tenant', 'platform', 'system'));
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}organization_scope_kind_immutable
BEFORE UPDATE OF scope_kind ON {{.Prefix}}iam_tenants
WHEN NEW.scope_kind IS NOT OLD.scope_kind
BEGIN
    SELECT RAISE(ABORT, 'administration scope kind is immutable');
END;
-- +goose StatementEnd

-- +goose Down
-- An older build must not reinterpret an administrative root as a tenant.
UPDATE {{.Prefix}}iam_tenants SET scope_kind = 'tenant' WHERE scope_kind <> 'tenant';
DROP TRIGGER {{.Prefix}}organization_scope_kind_immutable;
ALTER TABLE {{.Prefix}}iam_tenants DROP COLUMN scope_kind;
