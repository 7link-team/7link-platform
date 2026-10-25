-- +goose Up
SET @lane_n_target_check = (SELECT tc.CONSTRAINT_NAME FROM information_schema.TABLE_CONSTRAINTS tc JOIN information_schema.CHECK_CONSTRAINTS cc ON cc.CONSTRAINT_SCHEMA=tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME=tc.CONSTRAINT_NAME WHERE tc.TABLE_SCHEMA=DATABASE() AND tc.TABLE_NAME='{{.Prefix}}intake_records' AND tc.CONSTRAINT_TYPE='CHECK' AND cc.CHECK_CLAUSE LIKE '%CONVERTED%' AND cc.CHECK_CLAUSE LIKE '%requirement_id%');
SET @lane_n_target_ddl = CONCAT('ALTER TABLE {{.Prefix}}intake_records DROP CHECK `',REPLACE(@lane_n_target_check,'`','``'),'`');
PREPARE lane_n_target_statement FROM @lane_n_target_ddl;
EXECUTE lane_n_target_statement;
DEALLOCATE PREPARE lane_n_target_statement;
ALTER TABLE {{.Prefix}}intake_records ADD CONSTRAINT {{.Prefix}}ck_intake_converted_target CHECK(status<>'CONVERTED' OR kind='REWORK' OR requirement_id IS NOT NULL);
-- +goose Down
ALTER TABLE {{.Prefix}}intake_records ADD CONSTRAINT {{.Prefix}}ck_intake_legacy_target CHECK(status<>'CONVERTED' OR requirement_id IS NOT NULL);
ALTER TABLE {{.Prefix}}intake_records DROP CHECK {{.Prefix}}ck_intake_converted_target;
