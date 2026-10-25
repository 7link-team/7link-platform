-- +goose Up
CREATE TABLE {{.Prefix}}planning_goal_snapshots (
 tenant_id BIGINT NOT NULL, goal_id BIGINT NOT NULL, version INT NOT NULL,
 content TEXT NOT NULL, content_hash VARCHAR(64) NOT NULL, state VARCHAR(20) NOT NULL,
 observed_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,goal_id,version),
 CONSTRAINT {{.Prefix}}ck_goal_snapshot_version CHECK (version>0),
 CONSTRAINT {{.Prefix}}fk_goal_snapshot FOREIGN KEY (tenant_id,goal_id) REFERENCES {{.Prefix}}planning_goals (tenant_id,id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_goal_snapshot_time ON {{.Prefix}}planning_goal_snapshots(tenant_id,goal_id,observed_at,version);
-- +goose Down
CREATE TABLE {{.Prefix}}planning_goal_snapshot_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}planning_goal_snapshot_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}planning_goal_snapshots;
DROP TABLE {{.Prefix}}planning_goal_snapshot_rollback_guard;
DROP TABLE {{.Prefix}}planning_goal_snapshots;
