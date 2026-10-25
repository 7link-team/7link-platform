-- +goose Up
CREATE TABLE {{.Prefix}}planning_heavy_runs (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL,
 state VARCHAR(24) NOT NULL CHECK(state IN ('DRAFT','UNAVAILABLE','LAUNCHED','CANCEL_REQUESTED')),
 reason TEXT NOT NULL,
 branches TEXT NOT NULL,
 created_by BIGINT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME(3) NOT NULL
);
CREATE INDEX {{.Prefix}}planning_heavy_work ON {{.Prefix}}planning_heavy_runs(tenant_id,project_id,work_item_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}planning_heavy_runs;
