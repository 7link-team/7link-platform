-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_dispatch_actions (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,
 proposal_id BIGINT NOT NULL,proposal_version INTEGER NOT NULL CHECK(proposal_version>0),
 work_item_id BIGINT NOT NULL,work_version INTEGER NOT NULL CHECK(work_version>0),
 action VARCHAR(32) NOT NULL CHECK(action IN ('CONFIRM','OVERRIDE','DISMISS','EXPIRE','AUTO_APPLY')),
 inputs_digest VARCHAR(64) NOT NULL,selected_id BIGINT NULL,assignment_id BIGINT NULL,
 actor_id BIGINT NOT NULL,authority_source VARCHAR(256) NOT NULL,reason TEXT NOT NULL,
 created_at TIMESTAMPTZ(3) NOT NULL,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,proposal_id,proposal_version),
 CHECK ((action IN ('CONFIRM','OVERRIDE','AUTO_APPLY') AND selected_id IS NOT NULL AND assignment_id IS NOT NULL) OR (action IN ('DISMISS','EXPIRE') AND selected_id IS NULL AND assignment_id IS NULL))
);
CREATE INDEX {{.Prefix}}clientwork_dispatch_actions_project ON {{.Prefix}}clientwork_dispatch_actions(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_dispatch_actions;
