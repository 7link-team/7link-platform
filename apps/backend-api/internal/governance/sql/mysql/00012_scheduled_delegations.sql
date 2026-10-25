-- +goose Up
ALTER TABLE {{.Prefix}}governance_delegations ADD COLUMN activated_at DATETIME(3) NULL;
UPDATE {{.Prefix}}governance_delegations SET activated_at = starts_at;
-- +goose Down
ALTER TABLE {{.Prefix}}governance_delegations DROP COLUMN activated_at;
