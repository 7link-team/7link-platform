-- +goose Up
CREATE TABLE {{.Prefix}}knowledge_skills (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL,
 skill_key VARCHAR(64) NOT NULL, title VARCHAR(200) NOT NULL, description VARCHAR(1024) NOT NULL,
 state VARCHAR(32) NOT NULL CHECK (state IN ('ACTIVE','ARCHIVED')), current_version_id BIGINT NULL,
 version INTEGER NOT NULL CHECK (version >= 1), created_by BIGINT NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_knowledge_skills_key UNIQUE (tenant_id,skill_key)
);
CREATE TABLE {{.Prefix}}knowledge_skill_versions (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, skill_id BIGINT NOT NULL,
 version_no INTEGER NOT NULL CHECK (version_no >= 1), content TEXT NOT NULL, frontmatter TEXT NOT NULL,
 content_hash CHAR(64) NOT NULL, state VARCHAR(32) NOT NULL CHECK (state IN ('DRAFT','APPROVED','REJECTED','DEPRECATED')),
 origin VARCHAR(32) NOT NULL CHECK (origin IN ('MANUAL','DISTILLED')),
 source_work_item_id BIGINT NULL, source_execution_id BIGINT NULL,
 reviewed_by BIGINT NULL, reviewed_at DATETIME NULL, review_reason VARCHAR(2000) NOT NULL,
 created_by BIGINT NOT NULL, created_at DATETIME NOT NULL,
 CONSTRAINT {{.Prefix}}uq_knowledge_skill_versions_no UNIQUE (skill_id,version_no),
 CONSTRAINT {{.Prefix}}fk_knowledge_skill_versions_parent FOREIGN KEY (skill_id) REFERENCES {{.Prefix}}knowledge_skills(id)
);
CREATE TABLE {{.Prefix}}knowledge_agent_skills (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, agent_id BIGINT NOT NULL, skill_id BIGINT NOT NULL,
 pinned_version_id BIGINT NULL, attached_by BIGINT NOT NULL, attached_at DATETIME NOT NULL, live_slot SMALLINT NULL CHECK (live_slot IS NULL OR live_slot=1),
 CONSTRAINT {{.Prefix}}uq_knowledge_agent_skills_live UNIQUE (agent_id,skill_id,live_slot),
 CONSTRAINT {{.Prefix}}fk_knowledge_agent_skills_parent FOREIGN KEY (skill_id) REFERENCES {{.Prefix}}knowledge_skills(id),
 CONSTRAINT {{.Prefix}}fk_knowledge_agent_skills_pin FOREIGN KEY (pinned_version_id) REFERENCES {{.Prefix}}knowledge_skill_versions(id)
);
CREATE INDEX {{.Prefix}}ix_knowledge_skills_list ON {{.Prefix}}knowledge_skills(tenant_id,state,updated_at);
CREATE INDEX {{.Prefix}}ix_knowledge_agent_skills_agent ON {{.Prefix}}knowledge_agent_skills(tenant_id,agent_id,live_slot);
-- +goose Down
DROP TABLE {{.Prefix}}knowledge_agent_skills;
DROP TABLE {{.Prefix}}knowledge_skill_versions;
DROP TABLE {{.Prefix}}knowledge_skills;
