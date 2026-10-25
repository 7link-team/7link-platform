-- +goose Up
CREATE TABLE {{.Prefix}}planning_heavy_runs (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 project_id INTEGER NOT NULL,
 work_item_id INTEGER NOT NULL,
 state VARCHAR(24) NOT NULL CHECK(state IN ('DRAFT','UNAVAILABLE','LAUNCHED','CANCEL_REQUESTED')),
 reason TEXT NOT NULL,
 branches TEXT NOT NULL,
 created_by INTEGER NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL
);
CREATE INDEX {{.Prefix}}planning_heavy_work ON {{.Prefix}}planning_heavy_runs(tenant_id,project_id,work_item_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}planning_heavy_runs;
