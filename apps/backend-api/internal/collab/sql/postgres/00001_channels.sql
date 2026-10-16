-- +goose Up
CREATE TABLE {{.Prefix}}collab_channels (
 id BIGINT NOT NULL PRIMARY KEY, tenant_id BIGINT NOT NULL, project_id BIGINT NULL,
 kind VARCHAR(32) NOT NULL, name VARCHAR(40) NOT NULL, topic VARCHAR(200) NULL, direct_pair_key VARCHAR(48) NULL,
 last_seq BIGINT NOT NULL DEFAULT 0, last_message_at TIMESTAMPTZ(3) NULL, archived_at TIMESTAMPTZ(3) NULL, version INT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, created_by BIGINT NULL, updated_by BIGINT NULL,
 CONSTRAINT {{.Prefix}}chk_collab_channel_kind CHECK (kind IN ('PROJECT_DEFAULT','CUSTOM','AGENT_DIRECT','DIRECT')),
 CONSTRAINT {{.Prefix}}chk_collab_channel_project CHECK ((kind='DIRECT' AND project_id IS NULL) OR (kind<>'DIRECT' AND project_id IS NOT NULL))
);
CREATE UNIQUE INDEX {{.Prefix}}idx_collab_channel_default ON {{.Prefix}}collab_channels (project_id) WHERE kind='PROJECT_DEFAULT';
CREATE UNIQUE INDEX {{.Prefix}}idx_collab_channel_custom ON {{.Prefix}}collab_channels (project_id, lower(name)) WHERE kind='CUSTOM' AND archived_at IS NULL;
CREATE UNIQUE INDEX {{.Prefix}}idx_collab_channel_agent ON {{.Prefix}}collab_channels (project_id, direct_pair_key) WHERE kind='AGENT_DIRECT';
CREATE UNIQUE INDEX {{.Prefix}}idx_collab_channel_direct ON {{.Prefix}}collab_channels (tenant_id, direct_pair_key) WHERE kind='DIRECT';
CREATE INDEX {{.Prefix}}idx_collab_channel_scope ON {{.Prefix}}collab_channels (tenant_id, project_id, archived_at);
CREATE TABLE {{.Prefix}}collab_channel_members (
 channel_id BIGINT NOT NULL, principal_id BIGINT NOT NULL, tenant_id BIGINT NOT NULL,
 member_kind VARCHAR(32) NOT NULL, role VARCHAR(32) NOT NULL DEFAULT 'MEMBER', source VARCHAR(32) NOT NULL,
 last_read_seq BIGINT NOT NULL DEFAULT 0, muted BOOLEAN NOT NULL DEFAULT false,
 joined_at TIMESTAMPTZ(3) NOT NULL, left_at TIMESTAMPTZ(3) NULL, created_at TIMESTAMPTZ(3) NOT NULL, updated_at TIMESTAMPTZ(3) NOT NULL, created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY (channel_id, principal_id),
 CONSTRAINT {{.Prefix}}chk_collab_member_kind CHECK (member_kind IN ('HUMAN','AGENT')),
 CONSTRAINT {{.Prefix}}chk_collab_member_role CHECK (role IN ('OWNER','MEMBER')),
 CONSTRAINT {{.Prefix}}chk_collab_member_source CHECK (source IN ('DERIVED','EXPLICIT'))
);
CREATE INDEX {{.Prefix}}idx_collab_member_scope ON {{.Prefix}}collab_channel_members (tenant_id, principal_id, left_at);
-- +goose Down
DROP TABLE {{.Prefix}}collab_channel_members;
DROP TABLE {{.Prefix}}collab_channels;
