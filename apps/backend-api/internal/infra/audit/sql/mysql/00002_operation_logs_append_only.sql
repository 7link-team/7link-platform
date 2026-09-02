-- +goose Up
-- UPDATE/DELETE triggers do not stop TRUNCATE, and privileged accounts can
-- disable or drop them. Complete protection also requires least-privilege
-- application database credentials without TRUNCATE or trigger privileges.
-- MySQL DDL commits implicitly. Clear this module's trigger names first so a
-- retry can recover after a connection loss or a partially applied migration.
DROP TRIGGER IF EXISTS {{.Prefix}}trg_operation_logs_no_delete;
DROP TRIGGER IF EXISTS {{.Prefix}}trg_operation_logs_no_update;
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_update
BEFORE UPDATE ON {{.Prefix}}operation_logs FOR EACH ROW
BEGIN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'operation_logs is append-only';
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_delete
BEFORE DELETE ON {{.Prefix}}operation_logs FOR EACH ROW
BEGIN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'operation_logs is append-only';
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS {{.Prefix}}trg_operation_logs_no_delete;
DROP TRIGGER IF EXISTS {{.Prefix}}trg_operation_logs_no_update;
