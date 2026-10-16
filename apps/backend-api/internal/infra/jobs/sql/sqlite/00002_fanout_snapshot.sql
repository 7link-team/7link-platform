-- +goose Up
ALTER TABLE {{.Prefix}}background_jobs ADD COLUMN fanout_kind TEXT COLLATE BINARY NULL;
ALTER TABLE {{.Prefix}}background_jobs ADD COLUMN fanout_fire_ms BIGINT NULL CONSTRAINT {{.Prefix}}chk_jobs_fanout CHECK (
 (fanout_kind IS NULL AND fanout_fire_ms IS NULL) OR
 (fanout_kind IS NOT NULL AND fanout_fire_ms IS NOT NULL AND length(fanout_kind) BETWEEN 1 AND 128 AND fanout_kind <> 'jobs.fan_out' AND fanout_fire_ms >= 0 AND kind = 'jobs.fan_out' AND tenant_id = 0 AND concurrency_key IS NOT NULL AND length(concurrency_key) > 0)
);
-- +goose Down
ALTER TABLE {{.Prefix}}background_jobs DROP COLUMN fanout_fire_ms;
ALTER TABLE {{.Prefix}}background_jobs DROP COLUMN fanout_kind;
