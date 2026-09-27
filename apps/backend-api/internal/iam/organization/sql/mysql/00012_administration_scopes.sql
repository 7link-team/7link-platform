-- +goose Up
ALTER TABLE {{.Prefix}}iam_tenants ADD COLUMN scope_kind VARCHAR(16) COLLATE utf8mb4_bin NOT NULL DEFAULT 'tenant';
ALTER TABLE {{.Prefix}}iam_tenants ADD CONSTRAINT {{.Prefix}}chk_organization_scope_kind
    CHECK (scope_kind IN ('tenant', 'platform', 'system'));
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}organization_scope_kind_immutable
BEFORE UPDATE ON {{.Prefix}}iam_tenants FOR EACH ROW
BEGIN
    IF NOT (NEW.scope_kind <=> OLD.scope_kind) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'administration scope kind is immutable';
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
-- An older build must not reinterpret an administrative root as a tenant.
UPDATE {{.Prefix}}iam_tenants SET scope_kind = 'tenant' WHERE scope_kind <> 'tenant';
DROP TRIGGER {{.Prefix}}organization_scope_kind_immutable;
ALTER TABLE {{.Prefix}}iam_tenants DROP CHECK {{.Prefix}}chk_organization_scope_kind;
ALTER TABLE {{.Prefix}}iam_tenants DROP COLUMN scope_kind;
