-- +goose Up
CREATE TABLE {{.Prefix}}execution_project_policies (
 tenant_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL,
 allow_full_auto BOOLEAN NOT NULL,
 version INT NOT NULL CHECK(version>0),
 actor_id BIGINT NOT NULL,
 reason TEXT NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,project_id)
);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}execution_policy_rollback_guard (n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}execution_policy_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}execution_project_policies;
DROP TABLE {{.Prefix}}execution_policy_rollback_guard;
DROP TABLE {{.Prefix}}execution_project_policies;
