-- +goose Up
-- +goose StatementBegin
DO $$ DECLARE constraint_name TEXT; BEGIN
 SELECT conname INTO STRICT constraint_name FROM pg_constraint
 WHERE conrelid='{{.Prefix}}intake_records'::regclass AND contype='c'
 AND pg_get_constraintdef(oid) LIKE '%CONVERTED%' AND pg_get_constraintdef(oid) LIKE '%requirement_id%';
 EXECUTE format('ALTER TABLE {{.Prefix}}intake_records DROP CONSTRAINT %I',constraint_name);
END $$;
-- +goose StatementEnd
ALTER TABLE {{.Prefix}}intake_records ADD CONSTRAINT {{.Prefix}}ck_intake_converted_target CHECK(status<>'CONVERTED' OR kind='REWORK' OR requirement_id IS NOT NULL);
-- +goose Down
ALTER TABLE {{.Prefix}}intake_records ADD CONSTRAINT {{.Prefix}}ck_intake_legacy_target CHECK(status<>'CONVERTED' OR requirement_id IS NOT NULL);
ALTER TABLE {{.Prefix}}intake_records DROP CONSTRAINT {{.Prefix}}ck_intake_converted_target;
