-- +goose Up
ALTER TABLE {{.Prefix}}planning_reports ADD COLUMN needs_recheck BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE {{.Prefix}}planning_reports ADD COLUMN last_check_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE {{.Prefix}}planning_settings ADD COLUMN team_owner_id BIGINT NOT NULL DEFAULT 0;
-- +goose Down
ALTER TABLE {{.Prefix}}planning_settings DROP COLUMN team_owner_id;
ALTER TABLE {{.Prefix}}planning_reports DROP COLUMN last_check_hash;
ALTER TABLE {{.Prefix}}planning_reports DROP COLUMN needs_recheck;
