-- Only workflow metadata is retained; completed status is derived from the lifecycle tombstone.
-- +goose Up
CREATE TABLE {{.Prefix}}iam_closure_requests (
    principal_id BIGINT NOT NULL PRIMARY KEY,
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'processing', 'canceled', 'rejected')),
    version BIGINT NOT NULL CHECK (version >= 1),
    requested_at TIMESTAMPTZ(3) NOT NULL,
    updated_at TIMESTAMPTZ(3) NOT NULL,
    CONSTRAINT {{.Prefix}}fk_closure_request_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE RESTRICT
);
CREATE INDEX {{.Prefix}}idx_closure_request_queue ON {{.Prefix}}iam_closure_requests (status, updated_at, principal_id);

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}identity_closure_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.principal_id <> OLD.principal_id THEN
        RAISE EXCEPTION 'closure request ownership is immutable';
    END IF;
    PERFORM id FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
    IF EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL) THEN
        RAISE EXCEPTION 'principal is permanently anonymized';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}identity_closure_guard BEFORE INSERT OR UPDATE ON {{.Prefix}}iam_closure_requests FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}identity_closure_guard();

-- +goose Down
DROP TABLE {{.Prefix}}iam_closure_requests;
DROP FUNCTION {{.Prefix}}identity_closure_guard();
