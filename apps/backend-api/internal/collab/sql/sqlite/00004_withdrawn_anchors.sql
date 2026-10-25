-- +goose Up
ALTER TABLE {{.Prefix}}collab_messages ADD COLUMN withdrawn_at DATETIME NULL;

-- +goose Down
ALTER TABLE {{.Prefix}}collab_messages DROP COLUMN withdrawn_at;
