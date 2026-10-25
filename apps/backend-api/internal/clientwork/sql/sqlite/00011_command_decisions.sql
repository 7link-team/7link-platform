-- +goose Up
CREATE TABLE {{.Prefix}}clientwork_command_decisions (
 tenant_id BIGINT NOT NULL,id BIGINT NOT NULL,project_id BIGINT NOT NULL,
 target_kind VARCHAR(32) NOT NULL CHECK(target_kind IN ('PROJECT','WORK_ITEM')),
 target_id BIGINT NOT NULL,action VARCHAR(32) NOT NULL CHECK(action IN ('CANCEL','ASSIGN','REASSIGN')),
 source_id BIGINT NOT NULL,source_version INTEGER NOT NULL CHECK(source_version>0),
 authority_source VARCHAR(256) NOT NULL,actor_id BIGINT NOT NULL,reason TEXT NOT NULL,created_at DATETIME NOT NULL,
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,source_id,source_version,action)
);
CREATE INDEX {{.Prefix}}clientwork_decisions_project ON {{.Prefix}}clientwork_command_decisions(tenant_id,project_id,id);
-- +goose Down
DROP TABLE {{.Prefix}}clientwork_command_decisions;
