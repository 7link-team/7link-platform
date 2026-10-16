-- +goose Up
ALTER TABLE {{.Prefix}}background_jobs ADD COLUMN fanout_kind VARCHAR(128) COLLATE utf8mb4_bin NULL;
ALTER TABLE {{.Prefix}}background_jobs ADD COLUMN fanout_fire_ms BIGINT NULL;
ALTER TABLE {{.Prefix}}background_jobs ADD CONSTRAINT {{.Prefix}}chk_jobs_fanout CHECK (
 (fanout_kind IS NULL AND fanout_fire_ms IS NULL) OR
 (fanout_kind IS NOT NULL AND fanout_fire_ms IS NOT NULL AND char_length(fanout_kind) BETWEEN 1 AND 128 AND fanout_kind <> 'jobs.fan_out' AND fanout_fire_ms >= 0 AND kind = 'jobs.fan_out' AND tenant_id = 0 AND concurrency_key IS NOT NULL AND char_length(concurrency_key) > 0)
);
-- +goose Down
ALTER TABLE {{.Prefix}}background_jobs DROP CHECK {{.Prefix}}chk_jobs_fanout;
ALTER TABLE {{.Prefix}}background_jobs DROP COLUMN fanout_fire_ms;
ALTER TABLE {{.Prefix}}background_jobs DROP COLUMN fanout_kind;
