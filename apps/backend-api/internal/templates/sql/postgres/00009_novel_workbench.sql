-- +goose Up
ALTER TABLE {{.Prefix}}templates_studio_records DROP CONSTRAINT {{.Prefix}}tpl_studio_kind_ck;
ALTER TABLE {{.Prefix}}templates_studio_records ADD CONSTRAINT {{.Prefix}}tpl_studio_kind_ck CHECK (kind IN ('SHOT','CANDIDATE','QC_NOTE','CHAPTER','CONTINUITY','FORESHADOW','ASSISTANT_DRAFT','TASK','STORY_BIBLE'));
-- +goose Down
ALTER TABLE {{.Prefix}}templates_studio_records DROP CONSTRAINT {{.Prefix}}tpl_studio_kind_ck;
ALTER TABLE {{.Prefix}}templates_studio_records ADD CONSTRAINT {{.Prefix}}tpl_studio_kind_ck CHECK (kind IN ('SHOT','CANDIDATE','QC_NOTE','CHAPTER','CONTINUITY','FORESHADOW','ASSISTANT_DRAFT','TASK'));
