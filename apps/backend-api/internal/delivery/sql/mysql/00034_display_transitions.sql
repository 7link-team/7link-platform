-- +goose Up
ALTER TABLE {{.Prefix}}delivery_work_item_transitions ADD COLUMN from_state_key VARCHAR(40) NULL;
ALTER TABLE {{.Prefix}}delivery_work_item_transitions ADD COLUMN to_state_key VARCHAR(40) NULL;
ALTER TABLE {{.Prefix}}delivery_work_item_transitions ADD COLUMN comment TEXT NULL;

-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}delivery_display_transition_rollback_guard (n INT NOT NULL CHECK (n=0));
INSERT INTO {{.Prefix}}delivery_display_transition_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}delivery_work_item_transitions WHERE from_state_key IS NOT NULL OR to_state_key IS NOT NULL OR comment IS NOT NULL;
DROP TABLE {{.Prefix}}delivery_display_transition_rollback_guard;
ALTER TABLE {{.Prefix}}delivery_work_item_transitions DROP COLUMN comment;
ALTER TABLE {{.Prefix}}delivery_work_item_transitions DROP COLUMN to_state_key;
ALTER TABLE {{.Prefix}}delivery_work_item_transitions DROP COLUMN from_state_key;
