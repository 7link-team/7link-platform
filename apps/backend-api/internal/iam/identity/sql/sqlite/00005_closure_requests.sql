-- Only workflow metadata is retained; completed status is derived from the lifecycle tombstone.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_closure_requests (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'processing', 'canceled', 'rejected')),
    version BIGINT NOT NULL CHECK (version >= 1),
    requested_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT {{.Prefix}}fk_closure_request_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_closure_request_queue ON {{.Prefix}}iam_closure_requests (status, updated_at, principal_id);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}identity_closure_insert
BEFORE INSERT ON {{.Prefix}}iam_closure_requests
WHEN EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'closure request ownership is immutable and tombstones cannot be changed');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}identity_closure_update
BEFORE UPDATE ON {{.Prefix}}iam_closure_requests
WHEN NEW.principal_id != OLD.principal_id OR EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'closure request ownership is immutable and tombstones cannot be changed');
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_closure_requests;
