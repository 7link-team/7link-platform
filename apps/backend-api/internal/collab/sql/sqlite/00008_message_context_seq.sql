-- +goose Up
ALTER TABLE {{.Prefix}}collab_messages ADD COLUMN context_seq BIGINT NOT NULL DEFAULT 0;
UPDATE {{.Prefix}}collab_messages SET context_seq = seq;
-- +goose Down
ALTER TABLE {{.Prefix}}collab_messages DROP COLUMN context_seq;
