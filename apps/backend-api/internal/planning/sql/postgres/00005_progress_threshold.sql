-- +goose Up
ALTER TABLE {{.Prefix}}planning_settings ADD COLUMN watermelon_lag_threshold DECIMAL(5,4) NOT NULL DEFAULT 0.3000 CHECK (watermelon_lag_threshold >= 0.1000 AND watermelon_lag_threshold <= 0.6000);
-- +goose Down
ALTER TABLE {{.Prefix}}planning_settings DROP COLUMN watermelon_lag_threshold;
