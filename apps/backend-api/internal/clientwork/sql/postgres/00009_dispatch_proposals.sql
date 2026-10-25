-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_dispatch_proposals (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,work_item_id BIGINT NOT NULL,active_slot BIGINT NULL,
 work_version INTEGER NOT NULL CHECK(work_version>0),trigger VARCHAR(32) NOT NULL,
 state VARCHAR(32) NOT NULL CHECK(state IN ('PROPOSED','APPLIED','OVERRIDDEN','DISMISSED','EXPIRED')),
 result VARCHAR(32) NOT NULL CHECK(result IN ('RECOMMENDED','NO_ELIGIBLE')),
 snapshot TEXT NOT NULL,inputs_digest VARCHAR(64) NOT NULL,recommended_id BIGINT NULL,applied_id BIGINT NULL,assignment_id BIGINT NULL,
 reason TEXT NULL,decided_by BIGINT NULL,expires_at TIMESTAMPTZ NOT NULL,created_at TIMESTAMPTZ NOT NULL,decided_at TIMESTAMPTZ NULL,
 version INTEGER NOT NULL CHECK(version>0),PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,active_slot)
);
CREATE INDEX {{.Prefix}}clientwork_dispatch_page ON {{.Prefix}}clientwork_dispatch_proposals(tenant_id,project_id,id);
CREATE INDEX {{.Prefix}}clientwork_dispatch_work ON {{.Prefix}}clientwork_dispatch_proposals(tenant_id,work_item_id,created_at);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_dispatch_proposals;
