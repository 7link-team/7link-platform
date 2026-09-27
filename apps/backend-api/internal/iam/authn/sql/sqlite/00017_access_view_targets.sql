-- +goose Up
ALTER TABLE {{.Prefix}}iam_account_view_tokens ADD COLUMN target_type VARCHAR(16) NOT NULL DEFAULT 'account'
    CHECK (target_type = 'account' OR (target_type IN ('platform', 'tenant') AND target_type = scope_type AND principal_id = actor_id));
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_access_view_target_immutable
BEFORE UPDATE OF target_type ON {{.Prefix}}iam_account_view_tokens
BEGIN
    SELECT RAISE(ABORT, 'access view target is immutable') WHERE NEW.target_type != OLD.target_type;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER {{.Prefix}}authn_access_view_target_immutable;
ALTER TABLE {{.Prefix}}iam_account_view_tokens DROP COLUMN target_type;
