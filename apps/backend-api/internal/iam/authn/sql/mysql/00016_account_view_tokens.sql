-- +goose Up
CREATE TABLE {{.Prefix}}iam_account_view_tokens (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    token_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL CHECK (length(token_hash) = 64),
    actor_id BIGINT NOT NULL CHECK (actor_id > 0),
    principal_id BIGINT NOT NULL CHECK (principal_id > 0),
    actor_session_hash VARCHAR(64) COLLATE utf8mb4_bin NOT NULL CHECK (length(actor_session_hash) = 64),
    actor_credential_version BIGINT NOT NULL CHECK (actor_credential_version >= 0),
    target_credential_version BIGINT NOT NULL CHECK (target_credential_version >= 0),
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('system', 'platform', 'tenant', 'entity', 'personal')),
    scope_id BIGINT NOT NULL CHECK (scope_id >= 0),
    mode VARCHAR(16) NOT NULL DEFAULT 'read_only' CHECK (mode IN ('read_only', 'read_write')),
    created_at DATETIME(3) NOT NULL,
    expires_at DATETIME(3) NOT NULL,
    last_used_at DATETIME(3) NULL,
    revoked_at DATETIME(3) NULL,
    CONSTRAINT {{.Prefix}}ck_account_view_lifetime CHECK (expires_at > created_at),
    CONSTRAINT {{.Prefix}}uq_account_view_token_hash UNIQUE (token_hash),
    CONSTRAINT {{.Prefix}}fk_account_view_actor FOREIGN KEY (actor_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_account_view_target FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE INDEX {{.Prefix}}idx_account_view_actor ON {{.Prefix}}iam_account_view_tokens (actor_id, id);
CREATE INDEX {{.Prefix}}idx_account_view_target ON {{.Prefix}}iam_account_view_tokens (principal_id, id);
CREATE INDEX {{.Prefix}}idx_account_view_session ON {{.Prefix}}iam_account_view_tokens (actor_session_hash);

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_account_views_pe_i
BEFORE INSERT ON {{.Prefix}}iam_account_view_tokens
FOR EACH ROW
BEGIN
    DECLARE locked_principal BIGINT DEFAULT NULL;
    DECLARE erased_first DATETIME(3) DEFAULT NULL;
    DECLARE erased_second DATETIME(3) DEFAULT NULL;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = LEAST(NEW.actor_id, NEW.principal_id) FOR UPDATE;
    SELECT id INTO locked_principal FROM {{.Prefix}}iam_principals WHERE id = GREATEST(NEW.actor_id, NEW.principal_id) FOR UPDATE;
    SELECT anonymized_at INTO erased_first FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = LEAST(NEW.actor_id, NEW.principal_id) FOR UPDATE;
    SELECT anonymized_at INTO erased_second FROM {{.Prefix}}iam_principal_lifecycles WHERE principal_id = GREATEST(NEW.actor_id, NEW.principal_id) FOR UPDATE;
    IF erased_first IS NOT NULL OR erased_second IS NOT NULL THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'principal is permanently anonymized';
    END IF;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER {{.Prefix}}authn_account_views_immutable
BEFORE UPDATE ON {{.Prefix}}iam_account_view_tokens
FOR EACH ROW
BEGIN
    IF NEW.id != OLD.id OR NEW.token_hash != OLD.token_hash
        OR NEW.actor_id != OLD.actor_id OR NEW.principal_id != OLD.principal_id
        OR NEW.actor_session_hash != OLD.actor_session_hash
        OR NEW.actor_credential_version != OLD.actor_credential_version
        OR NEW.target_credential_version != OLD.target_credential_version
        OR NEW.scope_type != OLD.scope_type OR NEW.scope_id != OLD.scope_id
        OR NEW.mode != OLD.mode OR NEW.created_at != OLD.created_at
        OR NEW.expires_at != OLD.expires_at
        OR (OLD.revoked_at IS NOT NULL AND NOT (NEW.revoked_at <=> OLD.revoked_at)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'account view authorization is immutable';
    END IF;
END;
-- +goose StatementEnd

-- +goose Down
DROP TABLE {{.Prefix}}iam_account_view_tokens;
