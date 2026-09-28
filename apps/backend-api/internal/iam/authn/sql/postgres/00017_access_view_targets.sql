-- +goose Up
ALTER TABLE {{.Prefix}}iam_account_view_tokens ADD COLUMN target_type VARCHAR(16) NOT NULL DEFAULT 'account',
    ADD CONSTRAINT {{.Prefix}}ck_access_view_target CHECK (target_type = 'account' OR (target_type IN ('platform', 'tenant') AND target_type = scope_type AND principal_id = actor_id));
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_access_view_target_immutable() RETURNS trigger AS $$
BEGIN
    IF NEW.target_type <> OLD.target_type THEN
        RAISE EXCEPTION 'access view target is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}authn_access_view_target_immutable BEFORE UPDATE ON {{.Prefix}}iam_account_view_tokens
    FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_access_view_target_immutable();
-- +goose Down
DROP TRIGGER {{.Prefix}}authn_access_view_target_immutable ON {{.Prefix}}iam_account_view_tokens;
DROP FUNCTION {{.Prefix}}authn_access_view_target_immutable();
ALTER TABLE {{.Prefix}}iam_account_view_tokens DROP COLUMN target_type;
