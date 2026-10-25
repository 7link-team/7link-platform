-- +goose Up
ALTER TABLE {{.Prefix}}agents_tool_entries ADD COLUMN credential_last4 VARCHAR(4) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}agents_tool_entries ADD COLUMN credential_rotated_at DATETIME NULL;
ALTER TABLE {{.Prefix}}agents_tool_entries ADD COLUMN upstream_auth_failures INT NOT NULL DEFAULT 0;
ALTER TABLE {{.Prefix}}agents_tool_entries ADD COLUMN upstream_auth_notified_at DATETIME NULL;
-- +goose Down
ALTER TABLE {{.Prefix}}agents_tool_entries DROP COLUMN upstream_auth_notified_at;
ALTER TABLE {{.Prefix}}agents_tool_entries DROP COLUMN upstream_auth_failures;
ALTER TABLE {{.Prefix}}agents_tool_entries DROP COLUMN credential_rotated_at;
ALTER TABLE {{.Prefix}}agents_tool_entries DROP COLUMN credential_last4;
