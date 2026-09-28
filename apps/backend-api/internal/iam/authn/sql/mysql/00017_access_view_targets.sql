-- +goose Up
ALTER TABLE {{.Prefix}}iam_account_view_tokens ADD COLUMN target_type VARCHAR(16) NOT NULL DEFAULT 'account';
-- The check names the new column, so it is added once the column exists; some
-- MySQL-compatible engines do not resolve a column added by the same statement.
ALTER TABLE {{.Prefix}}iam_account_view_tokens ADD CONSTRAINT {{.Prefix}}ck_access_view_target CHECK (target_type = 'account' OR (target_type IN ('platform', 'tenant') AND target_type = scope_type AND principal_id = actor_id));
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_access_view_target_immutable
BEFORE UPDATE ON {{.Prefix}}iam_account_view_tokens FOR EACH ROW
BEGIN
    IF NEW.target_type <> OLD.target_type THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'access view target is immutable';
    END IF;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER {{.Prefix}}authn_access_view_target_immutable;
ALTER TABLE {{.Prefix}}iam_account_view_tokens DROP CHECK {{.Prefix}}ck_access_view_target;
ALTER TABLE {{.Prefix}}iam_account_view_tokens DROP COLUMN target_type;
