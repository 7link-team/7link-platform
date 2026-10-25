-- +goose Up

CREATE TABLE {{.Prefix}}fleet_machines (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    principal_id BIGINT NOT NULL UNIQUE,
    name VARCHAR(100) NOT NULL,
    name_key VARCHAR(100),
    state VARCHAR(32) NOT NULL CHECK(state IN ('ONLINE','OFFLINE','DRAINING','REVOKED')),
    drain_requested BOOLEAN NOT NULL,
    os VARCHAR(32) NOT NULL CHECK(os IN ('WINDOWS','MACOS','LINUX')),
    slot_count INTEGER NOT NULL CHECK(slot_count BETWEEN 1 AND 8),
    conn_epoch BIGINT NOT NULL CHECK(conn_epoch >= 0),
    current_session_id BIGINT,
    last_seen_at DATETIME(3),
    version INTEGER NOT NULL CHECK(version > 0),
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    UNIQUE(tenant_id,name_key),
    UNIQUE(tenant_id,id),
    CHECK((state = 'REVOKED' AND name_key IS NULL) OR (state <> 'REVOKED' AND name_key IS NOT NULL))
);

CREATE TABLE {{.Prefix}}fleet_runner_sessions (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    machine_id BIGINT NOT NULL,
    incarnation_id VARCHAR(64) NOT NULL,
    conn_epoch BIGINT NOT NULL CHECK(conn_epoch > 0),
    state VARCHAR(32) NOT NULL CHECK(state IN ('ACTIVE','CLOSED','SUPERSEDED','EXPIRED')),
    created_at DATETIME(3) NOT NULL,
    last_seen_at DATETIME(3) NOT NULL,
    closed_at DATETIME(3),
    UNIQUE(tenant_id,machine_id,conn_epoch),
    FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);

CREATE TABLE {{.Prefix}}fleet_machine_runtimes (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    machine_id BIGINT NOT NULL,
    kind VARCHAR(32) NOT NULL CHECK(kind IN ('CODEX','CLAUDE_CODE')),
    version VARCHAR(64) NOT NULL,
    executable_path VARCHAR(1024) NOT NULL,
    models_json TEXT NOT NULL,
    inspected_at DATETIME(3) NOT NULL,
    UNIQUE(tenant_id,machine_id,kind),
    FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);

CREATE TABLE {{.Prefix}}fleet_workdirs (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    machine_id BIGINT NOT NULL,
    project_id BIGINT NOT NULL,
    repository_id BIGINT NOT NULL,
    path VARCHAR(1024) NOT NULL,
    path_key CHAR(64) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    UNIQUE(tenant_id,machine_id,project_id,repository_id),
    UNIQUE(tenant_id,machine_id,path_key),
    FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);

CREATE TABLE {{.Prefix}}fleet_slot_reservations (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    machine_id BIGINT NOT NULL,
    execution_id BIGINT NOT NULL,
    slot_no INTEGER NOT NULL CHECK(slot_no BETWEEN 1 AND 8),
    active_slot INTEGER,
    created_at DATETIME(3) NOT NULL,
    released_at DATETIME(3),
    UNIQUE(tenant_id,machine_id,active_slot),
    UNIQUE(tenant_id,execution_id),
    FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id),
    CHECK((released_at IS NULL AND active_slot = slot_no AND active_slot IS NOT NULL) OR (released_at IS NOT NULL AND active_slot IS NULL))
);

CREATE TABLE {{.Prefix}}fleet_machine_tools (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    machine_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    version VARCHAR(64) NOT NULL,
    executable_path VARCHAR(1024) NOT NULL,
    inspected_at DATETIME(3) NOT NULL,
    UNIQUE(tenant_id,machine_id,name),
    FOREIGN KEY(tenant_id,machine_id) REFERENCES {{.Prefix}}fleet_machines(tenant_id,id)
);

CREATE TABLE {{.Prefix}}fleet_model_prices (
    id BIGINT NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    runtime VARCHAR(32) NOT NULL CHECK(runtime IN ('CODEX','CLAUDE_CODE')),
    model VARCHAR(160) NOT NULL,
    input_micros BIGINT NOT NULL CHECK(input_micros >= 0),
    output_micros BIGINT NOT NULL CHECK(output_micros >= 0),
    updated_at DATETIME(3) NOT NULL,
    UNIQUE(tenant_id,runtime,model)
);

-- +goose Down

DROP TABLE {{.Prefix}}fleet_model_prices;

DROP TABLE {{.Prefix}}fleet_machine_tools;

DROP TABLE {{.Prefix}}fleet_slot_reservations;

DROP TABLE {{.Prefix}}fleet_workdirs;

DROP TABLE {{.Prefix}}fleet_machine_runtimes;

DROP TABLE {{.Prefix}}fleet_runner_sessions;

DROP TABLE {{.Prefix}}fleet_machines;
