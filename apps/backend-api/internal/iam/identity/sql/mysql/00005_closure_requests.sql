-- Only workflow metadata is retained; completed status is derived from the lifecycle tombstone.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_closure_requests (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'processing', 'canceled', 'rejected')),
    version BIGINT NOT NULL CHECK (version >= 1),
    requested_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    CONSTRAINT {{.Prefix}}fk_closure_request_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_closure_request_queue ON {{.Prefix}}iam_closure_requests (status, updated_at, principal_id);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}identity_closure_insert
BEFORE INSERT ON {{.Prefix}}iam_closure_requests FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = NEW.principal_id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}identity_closure_update
BEFORE UPDATE ON {{.Prefix}}iam_closure_requests FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id <> OLD.principal_id THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'closure request ownership is immutable';
    END IF;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
    SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = NEW.principal_id FOR UPDATE;
    IF erased IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_closure_requests;
