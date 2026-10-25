-- +goose Up
CREATE TABLE {{.Prefix}}governance_spawn_requests (
 id INTEGER NOT NULL PRIMARY KEY,
 tenant_id INTEGER NOT NULL,
 requester_id INTEGER NOT NULL,
 supervisor_id INTEGER NOT NULL,
 agent_id INTEGER,
 state VARCHAR(16) NOT NULL CHECK(state IN ('REQUESTED','UNAVAILABLE','ADOPTED','REJECTED','ORPHANED','EXPIRED')),
 payload TEXT NOT NULL,
 reason TEXT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL
);
CREATE INDEX {{.Prefix}}governance_spawn_request_supervisor ON {{.Prefix}}governance_spawn_requests(tenant_id,supervisor_id,state,id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_spawn_requests;
