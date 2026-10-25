-- +goose Up
ALTER TABLE {{.Prefix}}collab_channels ADD COLUMN visibility VARCHAR(32) NOT NULL DEFAULT 'INTERNAL' CHECK (visibility IN ('INTERNAL','CLIENT_SHARED'));
ALTER TABLE {{.Prefix}}collab_channel_members ADD COLUMN external BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE {{.Prefix}}collab_messages (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 seq BIGINT NOT NULL, author_id BIGINT NOT NULL, body TEXT NOT NULL,
 reply_to_message_id BIGINT NULL, visibility VARCHAR(32) NOT NULL,
 version INT NOT NULL DEFAULT 1, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}chk_collab_message_visibility CHECK (visibility IN ('INTERNAL','CHANNEL')),
 CONSTRAINT {{.Prefix}}chk_collab_message_seq CHECK (seq > 0),
 CONSTRAINT {{.Prefix}}uq_collab_message_seq UNIQUE (channel_id,seq)
);
CREATE INDEX {{.Prefix}}idx_collab_message_scope ON {{.Prefix}}collab_messages (tenant_id,channel_id,visibility,seq);
CREATE INDEX {{.Prefix}}idx_collab_message_thread ON {{.Prefix}}collab_messages (tenant_id,channel_id,reply_to_message_id,seq);
CREATE TABLE {{.Prefix}}collab_message_mentions (
 message_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL,
 PRIMARY KEY (message_id,principal_id)
);
CREATE INDEX {{.Prefix}}idx_collab_mention_member ON {{.Prefix}}collab_message_mentions (tenant_id,principal_id,channel_id,message_id);
CREATE TABLE {{.Prefix}}collab_saved_messages (
 message_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL, created_at DATETIME(3) NOT NULL,
 PRIMARY KEY (message_id,principal_id)
);
CREATE TABLE {{.Prefix}}collab_message_attachments (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, channel_id BIGINT NOT NULL, message_id BIGINT NOT NULL,
 file_id BIGINT NOT NULL, version_id BIGINT NOT NULL,
 CONSTRAINT {{.Prefix}}uq_collab_message_attachment UNIQUE(message_id,file_id)
);
-- +goose Down
DROP TABLE {{.Prefix}}collab_message_attachments;
DROP TABLE {{.Prefix}}collab_saved_messages;
DROP TABLE {{.Prefix}}collab_message_mentions;
DROP TABLE {{.Prefix}}collab_messages;
ALTER TABLE {{.Prefix}}collab_channel_members DROP COLUMN external;
ALTER TABLE {{.Prefix}}collab_channels DROP COLUMN visibility;
