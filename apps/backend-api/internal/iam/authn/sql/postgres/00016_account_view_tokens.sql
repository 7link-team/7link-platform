-- +goose Up
CREATE TABLE {{.Prefix}}iam_account_view_tokens (
    id BIGINT NOT NULL PRIMARY KEY CHECK (id > 0),
    token_hash VARCHAR(64) NOT NULL CHECK (length(token_hash) = 64),
    actor_id BIGINT NOT NULL CHECK (actor_id > 0),
    principal_id BIGINT NOT NULL CHECK (principal_id > 0),
    actor_session_hash VARCHAR(64) NOT NULL CHECK (length(actor_session_hash) = 64),
    actor_credential_version BIGINT NOT NULL CHECK (actor_credential_version >= 0),
    target_credential_version BIGINT NOT NULL CHECK (target_credential_version >= 0),
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('system', 'platform', 'tenant', 'entity', 'personal')),
    scope_id BIGINT NOT NULL CHECK (scope_id >= 0),
    mode VARCHAR(16) NOT NULL DEFAULT 'read_only' CHECK (mode IN ('read_only', 'read_write')),
    created_at TIMESTAMPTZ(3) NOT NULL,
    expires_at TIMESTAMPTZ(3) NOT NULL CHECK (expires_at > created_at),
    last_used_at TIMESTAMPTZ(3) NULL,
    revoked_at TIMESTAMPTZ(3) NULL,
    CONSTRAINT {{.Prefix}}uq_account_view_token_hash UNIQUE (token_hash),
    CONSTRAINT {{.Prefix}}fk_account_view_actor FOREIGN KEY (actor_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE,
    CONSTRAINT {{.Prefix}}fk_account_view_target FOREIGN KEY (principal_id) REFERENCES {{.Prefix}}iam_principals(id) ON DELETE CASCADE
);
CREATE INDEX {{.Prefix}}idx_account_view_actor ON {{.Prefix}}iam_account_view_tokens (actor_id, id);
CREATE INDEX {{.Prefix}}idx_account_view_target ON {{.Prefix}}iam_account_view_tokens (principal_id, id);
CREATE INDEX {{.Prefix}}idx_account_view_session ON {{.Prefix}}iam_account_view_tokens (actor_session_hash);

-- +goose StatementBegin
CREATE FUNCTION {{.Prefix}}authn_account_views_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM id FROM {{.Prefix}}iam_principals
            WHERE id IN (NEW.actor_id, NEW.principal_id) ORDER BY id FOR UPDATE;
        PERFORM principal_id FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id IN (NEW.actor_id, NEW.principal_id) ORDER BY principal_id FOR UPDATE;
        IF EXISTS (SELECT 1 FROM {{.Prefix}}iam_principal_lifecycles
            WHERE principal_id IN (NEW.actor_id, NEW.principal_id) AND anonymized_at IS NOT NULL) THEN
            RAISE EXCEPTION 'principal is permanently anonymized';
        END IF;
    ELSIF ROW(NEW.id, NEW.token_hash, NEW.actor_id, NEW.principal_id, NEW.actor_session_hash,
        NEW.actor_credential_version, NEW.target_credential_version, NEW.scope_type,
        NEW.scope_id, NEW.mode, NEW.created_at, NEW.expires_at)
        IS DISTINCT FROM ROW(OLD.id, OLD.token_hash, OLD.actor_id, OLD.principal_id, OLD.actor_session_hash,
        OLD.actor_credential_version, OLD.target_credential_version, OLD.scope_type,
        OLD.scope_id, OLD.mode, OLD.created_at, OLD.expires_at)
        OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
        RAISE EXCEPTION 'account view authorization is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER {{.Prefix}}authn_account_views_guard
BEFORE INSERT OR UPDATE ON {{.Prefix}}iam_account_view_tokens
FOR EACH ROW EXECUTE FUNCTION {{.Prefix}}authn_account_views_guard();

-- +goose Down
DROP TABLE {{.Prefix}}iam_account_view_tokens;
DROP FUNCTION {{.Prefix}}authn_account_views_guard();
