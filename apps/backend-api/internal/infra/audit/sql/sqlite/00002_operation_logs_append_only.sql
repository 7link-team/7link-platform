-- +goose Up
-- UPDATE/DELETE triggers cannot protect maintenance paths outside ordinary DML,
-- and privileged accounts can disable or drop them. Complete protection also
-- requires least-privilege application database credentials.
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_update
BEFORE UPDATE ON {{.Prefix}}operation_logs
BEGIN
    SELECT RAISE(ABORT, 'operation_logs is append-only');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}trg_operation_logs_no_delete
BEFORE DELETE ON {{.Prefix}}operation_logs
BEGIN
    SELECT RAISE(ABORT, 'operation_logs is append-only');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER {{.Prefix}}trg_operation_logs_no_delete;
DROP TRIGGER {{.Prefix}}trg_operation_logs_no_update;
