-- +goose Up
CREATE TABLE {{.Prefix}}collab_system_cards (
 message_id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 source_id BIGINT NOT NULL, kind VARCHAR(32) NOT NULL,
 payload TEXT NOT NULL, fold_key VARCHAR(200) NOT NULL, attention BOOLEAN NOT NULL,
 UNIQUE (channel_id, source_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}collab_system_cards;
