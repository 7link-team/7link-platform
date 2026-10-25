-- +goose Up
CREATE TABLE {{.Prefix}}governance_spawn_requests (
 id BIGINT NOT NULL PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 requester_id BIGINT NOT NULL,
 supervisor_id BIGINT NOT NULL,
 agent_id BIGINT,
 state VARCHAR(16) NOT NULL CHECK(state IN ('REQUESTED','UNAVAILABLE','ADOPTED','REJECTED','ORPHANED','EXPIRED')),
 payload TEXT NOT NULL,
 reason TEXT NOT NULL,
 version INT NOT NULL CHECK(version > 0),
 created_at DATETIME(3) NOT NULL,
 expires_at DATETIME(3) NOT NULL
);
CREATE INDEX {{.Prefix}}governance_spawn_request_supervisor ON {{.Prefix}}governance_spawn_requests(tenant_id,supervisor_id,state,id);
-- +goose Down
DROP TABLE {{.Prefix}}governance_spawn_requests;
