-- +goose Up
DROP TABLE {{.Prefix}}notification_receipts;
-- +goose Down
CREATE TABLE {{.Prefix}}notification_receipts(event_id BIGINT NOT NULL PRIMARY KEY, created_at DATETIME NOT NULL);
