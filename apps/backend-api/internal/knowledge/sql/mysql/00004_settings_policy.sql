-- +goose Up
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN index_strategy VARCHAR(32) NOT NULL DEFAULT 'LEXICAL';
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN source_permission VARCHAR(32) NOT NULL DEFAULT 'WRITERS';
ALTER TABLE {{.Prefix}}knowledge_settings ADD COLUMN memory_approval VARCHAR(32) NOT NULL DEFAULT 'RULES_ONLY';

ALTER TABLE {{.Prefix}}knowledge_entries DROP CHECK {{.Prefix}}chk_knowledge_rule;
ALTER TABLE {{.Prefix}}knowledge_entries ADD CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind IN ('RULE','EXPERIENCE') OR state IN ('ACTIVE','REVOKED'));

-- +goose Down
ALTER TABLE {{.Prefix}}knowledge_entries DROP CHECK {{.Prefix}}chk_knowledge_rule;
ALTER TABLE {{.Prefix}}knowledge_entries ADD CONSTRAINT {{.Prefix}}chk_knowledge_rule CHECK (kind='RULE' OR state IN ('ACTIVE','REVOKED'));

ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN memory_approval;
ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN source_permission;
ALTER TABLE {{.Prefix}}knowledge_settings DROP COLUMN index_strategy;
