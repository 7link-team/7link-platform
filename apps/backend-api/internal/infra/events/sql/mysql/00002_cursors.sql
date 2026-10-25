-- +goose Up
CREATE TABLE {{.Prefix}}domain_event_cursors (
 id BIGINT NOT NULL PRIMARY KEY, consumer VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 partition_no INTEGER NOT NULL DEFAULT 0, last_seq BIGINT NOT NULL DEFAULT 0,
 fence_token BIGINT NOT NULL DEFAULT 0, paused BOOLEAN NOT NULL DEFAULT false,
 updated_at DATETIME(3) NOT NULL,
 CONSTRAINT {{.Prefix}}uq_domain_event_cursor UNIQUE (consumer, partition_no),
 CONSTRAINT {{.Prefix}}chk_domain_event_cursor CHECK (partition_no BETWEEN 0 AND 15 AND last_seq>=0 AND fence_token>=0)
);
-- +goose Down
DROP TABLE {{.Prefix}}domain_event_cursors;
