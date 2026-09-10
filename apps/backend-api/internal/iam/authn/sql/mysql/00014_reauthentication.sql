-- +goose Up
CREATE TABLE {{.Prefix}}iam_reauth_challenges (
    id_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    session_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL,
    credential_version BIGINT NOT NULL,
    purpose VARCHAR(64) NOT NULL,
    target VARCHAR(512) NOT NULL,
    method VARCHAR(32) NOT NULL,
    session_data TEXT NOT NULL,
    created_at DATETIME(3) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    verified_at DATETIME(3) NULL,
    consumed_at DATETIME(3) NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT {{.Prefix}}fk_reauth_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_reauth_principal ON {{.Prefix}}iam_reauth_challenges (principal_id, expires_at);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_i
BEFORE INSERT ON {{.Prefix}}iam_reauth_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_reauth_challenges
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased DATETIME(3) DEFAULT NULL;
    IF OLD.principal_id IS NOT NULL AND NOT (NEW.principal_id <=> OLD.principal_id) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal ownership is immutable';
    END IF;
    IF NEW.principal_id IS NOT NULL THEN
        SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = NEW.principal_id FOR UPDATE;
        SELECT anonymized_at INTO erased FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id FOR UPDATE;
        IF erased IS NOT NULL AND 1 = 1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
        END IF;
    END IF;
END;
-- +goose StatementEnd

-- +goose Down

DROP TABLE {{.Prefix}}iam_reauth_challenges;
