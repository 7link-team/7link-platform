-- +goose Up
CREATE TABLE {{.Prefix}}planning_goal_contributors (
 tenant_id BIGINT NOT NULL,
 id BIGINT NOT NULL,
 goal_id BIGINT NOT NULL,
 kr VARCHAR(64) NOT NULL,
 principal_id BIGINT NOT NULL,
 created_by BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),
 CONSTRAINT {{.Prefix}}planning_contributor_unique UNIQUE(tenant_id,goal_id,kr,principal_id),
 CONSTRAINT {{.Prefix}}planning_contributor_goal FOREIGN KEY(tenant_id,goal_id) REFERENCES {{.Prefix}}planning_goals(tenant_id,id),
 CONSTRAINT {{.Prefix}}planning_contributor_kr_length CHECK(CHAR_LENGTH(kr) BETWEEN 1 AND 64)
);
-- +goose Down
DROP TABLE {{.Prefix}}planning_goal_contributors;
