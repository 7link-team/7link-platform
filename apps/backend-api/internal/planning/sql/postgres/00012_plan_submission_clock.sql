-- +goose Up
ALTER TABLE {{.Prefix}}planning_plans ADD COLUMN submitted_at TIMESTAMPTZ(3) NULL;
ALTER TABLE {{.Prefix}}planning_plans ADD COLUMN unconfirmed_at TIMESTAMPTZ(3) NULL;
-- Existing submitted rows retain the best available pre-migration clock.
UPDATE {{.Prefix}}planning_plans SET submitted_at=updated_at WHERE state='SUBMITTED';
-- +goose Down
ALTER TABLE {{.Prefix}}planning_plans DROP COLUMN unconfirmed_at;
ALTER TABLE {{.Prefix}}planning_plans DROP COLUMN submitted_at;
