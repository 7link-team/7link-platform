-- +goose Up
CREATE TABLE {{.Prefix}}collab_message_revisions (
 message_id BIGINT NOT NULL, revision INT NOT NULL, tenant_id BIGINT NOT NULL,
 body TEXT NOT NULL, edited_by BIGINT NOT NULL, edited_at DATETIME(3) NOT NULL,
 PRIMARY KEY (message_id, revision)
);
-- +goose Down
DROP TABLE {{.Prefix}}collab_message_revisions;
