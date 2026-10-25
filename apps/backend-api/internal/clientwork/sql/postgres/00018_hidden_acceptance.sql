-- +goose Up
ALTER TABLE {{.Prefix}}clientwork_test_cases ADD COLUMN is_hidden_acceptance BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
-- A rollback must not silently make confidential cases readable by old code.
CREATE TEMPORARY TABLE IF NOT EXISTS {{.Prefix}}clientwork_hidden_rollback_guard (
 allow_drop INTEGER NOT NULL CHECK (allow_drop = 1)
);
INSERT INTO {{.Prefix}}clientwork_hidden_rollback_guard (allow_drop)
SELECT 0 FROM {{.Prefix}}clientwork_test_cases WHERE is_hidden_acceptance = TRUE LIMIT 1;
DROP TABLE {{.Prefix}}clientwork_hidden_rollback_guard;
ALTER TABLE {{.Prefix}}clientwork_test_cases DROP COLUMN is_hidden_acceptance;
