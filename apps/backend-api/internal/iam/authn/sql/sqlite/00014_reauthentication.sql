-- +goose Up
CREATE TABLE {{.Prefix}}iam_reauth_challenges (
    id_hash VARCHAR(64) COLLATE BINARY NOT NULL PRIMARY KEY,
    principal_id BIGINT NOT NULL,
    session_hash VARCHAR(64) COLLATE BINARY NOT NULL,
    credential_version BIGINT NOT NULL,
    purpose VARCHAR(64) NOT NULL,
    target VARCHAR(512) NOT NULL,
    method VARCHAR(32) NOT NULL,
    session_data TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    verified_at DATETIME NULL,
    consumed_at DATETIME NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT {{.Prefix}}fk_reauth_principal FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_reauth_principal ON {{.Prefix}}iam_reauth_challenges (principal_id, expires_at);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_i
BEFORE INSERT ON {{.Prefix}}iam_reauth_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_reauth_pe_u
BEFORE UPDATE ON {{.Prefix}}iam_reauth_challenges
BEGIN
    SELECT RAISE(ABORT, 'principal ownership is immutable')
        WHERE OLD.principal_id IS NOT NULL AND NEW.principal_id IS NOT OLD.principal_id;
    SELECT RAISE(ABORT, 'principal is permanently anonymized')
        WHERE NEW.principal_id IS NOT NULL AND 1 = 1
        AND EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id = NEW.principal_id AND anonymized_at IS NOT NULL);
END;
-- +goose StatementEnd

-- +goose Down

DROP TABLE {{.Prefix}}iam_reauth_challenges;
