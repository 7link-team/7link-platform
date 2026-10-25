-- +goose Up
CREATE TABLE {{.Prefix}}collab_message_work_items (
 tenant_id BIGINT NOT NULL, message_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 project_id BIGINT NOT NULL, work_item_id BIGINT NOT NULL, created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 PRIMARY KEY (tenant_id,message_id),
 CONSTRAINT {{.Prefix}}uq_collab_message_work UNIQUE (tenant_id,work_item_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}collab_message_work_items;
