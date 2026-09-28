-- +goose Up
-- UPDATE/DELETE triggers do not stop TRUNCATE, and privileged accounts can
-- disable or drop them. Complete protection also requires least-privilege
-- application database credentials without TRUNCATE or trigger privileges.
-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}reject_operation_log_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'operation_logs is append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_update
BEFORE UPDATE ON {{.Prefix}}operation_logs
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}reject_operation_log_change();
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_delete
BEFORE DELETE ON {{.Prefix}}operation_logs
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}reject_operation_log_change();

-- +goose Down
DROP TRIGGER {{.Prefix}}trg_operation_logs_no_delete ON {{.Prefix}}operation_logs;
DROP TRIGGER {{.Prefix}}trg_operation_logs_no_update ON {{.Prefix}}operation_logs;
DROP FUNCTION {{.Prefix}}reject_operation_log_change();
