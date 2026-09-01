-- +goose Up
CREATE TABLE {{.Prefix}}operation_logs (
    id                 INTEGER PRIMARY KEY,
    occurred_at        DATETIME NOT NULL,
    duration_ms        REAL,
    entry              TEXT    NOT NULL,
    operation          TEXT    NOT NULL,
    access             TEXT    NOT NULL,
    action             TEXT,
    resource_type      TEXT,
    resource_id        BIGINT,
    actor_id           BIGINT,
    actor_type         TEXT,
    actor_name         TEXT,
    tenant_id          BIGINT,
    client_ip          TEXT,
    user_agent         TEXT,
    request_id         TEXT,
    session_id         TEXT,
    host               TEXT,
    target_method      TEXT,
    target_route       TEXT,
    outcome            TEXT    NOT NULL,
    status_code        INTEGER,
    code               INTEGER,
    message            TEXT,
    error              TEXT,
    request_body       TEXT,
    request_size       INTEGER,
    request_truncated  INTEGER DEFAULT 0,
    request_omitted    TEXT,
    response_body      TEXT,
    response_size      INTEGER,
    response_truncated INTEGER DEFAULT 0,
    response_omitted   TEXT,
    trace_id           TEXT,
    span_id            TEXT,
    service            TEXT,
    version            TEXT,
    instance           TEXT,
    metadata           TEXT,
    created_at         DATETIME
);

CREATE INDEX {{.Prefix}}idx_operation_logs_occurred_at ON {{.Prefix}}operation_logs (occurred_at);
CREATE INDEX {{.Prefix}}idx_operation_logs_actor_time ON {{.Prefix}}operation_logs (actor_id, occurred_at);
CREATE INDEX {{.Prefix}}idx_operation_logs_op_time    ON {{.Prefix}}operation_logs (operation, occurred_at);
CREATE INDEX {{.Prefix}}idx_operation_logs_trace      ON {{.Prefix}}operation_logs (trace_id);
CREATE INDEX {{.Prefix}}idx_operation_logs_resource   ON {{.Prefix}}operation_logs (resource_type, resource_id, occurred_at);

-- +goose Down
DROP TABLE {{.Prefix}}operation_logs;
