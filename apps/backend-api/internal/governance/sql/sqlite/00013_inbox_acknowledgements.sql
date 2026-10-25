-- +goose Up
CREATE TABLE {{.Prefix}}governance_inbox_acknowledgements (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 principal_id INTEGER NOT NULL,
 kind VARCHAR(64) NOT NULL CHECK(kind='CLARIFICATION_REPLY'),
 source_id INTEGER NOT NULL,
 source_revision VARCHAR(64) NOT NULL,
 created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}governance_inbox_ack_source UNIQUE(tenant_id,principal_id,kind,source_id,source_revision)
);
-- +goose Down
CREATE TABLE IF NOT EXISTS {{.Prefix}}governance_inbox_ack_rollback_guard(n INT NOT NULL CHECK(n=0));
INSERT INTO {{.Prefix}}governance_inbox_ack_rollback_guard(n) SELECT COUNT(*) FROM {{.Prefix}}governance_inbox_acknowledgements;
DROP TABLE {{.Prefix}}governance_inbox_ack_rollback_guard;
DROP TABLE {{.Prefix}}governance_inbox_acknowledgements;
