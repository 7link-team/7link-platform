-- +goose Up
CREATE TABLE {{.Prefix}}delivery_work_item_activities (
 tenant_id BIGINT NOT NULL, id BIGINT NOT NULL PRIMARY KEY, project_id BIGINT NOT NULL,
 work_item_id BIGINT NOT NULL, seq BIGINT NOT NULL, kind VARCHAR(32) NOT NULL,
 actor_kind VARCHAR(16) NOT NULL, actor_id BIGINT NOT NULL, ref_kind VARCHAR(32) NOT NULL,
 ref_id BIGINT NOT NULL, title VARCHAR(200) NOT NULL, occurred_at TIMESTAMPTZ(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_work_activity_seq UNIQUE(work_item_id,seq),
 CONSTRAINT {{.Prefix}}chk_work_activity_seq CHECK(seq > 0)
);
CREATE INDEX {{.Prefix}}idx_work_activity_scope ON {{.Prefix}}delivery_work_item_activities(tenant_id,work_item_id,seq);
-- +goose Down
DROP TABLE {{.Prefix}}delivery_work_item_activities;
