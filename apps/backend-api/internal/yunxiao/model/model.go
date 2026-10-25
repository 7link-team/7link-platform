package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Connection struct {
	ID                  guid.ID    `gorm:"column:id;primaryKey"`
	TenantID            guid.ID    `gorm:"column:tenant_id;not null"`
	Version             int        `gorm:"column:version;not null"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null"`
	CreatedBy           guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null"`
	UpdatedBy           guid.ID    `gorm:"column:updated_by;not null"`
	Provider            string     `gorm:"column:provider;size:16;not null"`
	Name                string     `gorm:"column:name;size:80;not null"`
	OrganizationID      string     `gorm:"column:organization_id;size:64;not null"`
	APIEndpoint         string     `gorm:"column:api_endpoint;size:200;not null"`
	TokenSealed         string     `gorm:"column:token_sealed;type:text;not null"`
	TokenLast4          string     `gorm:"column:token_last4;size:4;not null"`
	WebhookSecretSealed string     `gorm:"column:webhook_secret_sealed;type:text;not null"`
	State               string     `gorm:"column:state;size:20;not null"`
	LastCheckedAt       *time.Time `gorm:"column:last_checked_at"`
	LastError           *string    `gorm:"column:last_error;size:500"`
	TokenScopes         string     `gorm:"column:token_scopes;type:text;not null"`
}

func (Connection) TableName() string { return "integration_connections" }

type ProjectMapping struct {
	ID                  guid.ID   `gorm:"column:id;primaryKey"`
	TenantID            guid.ID   `gorm:"column:tenant_id;not null"`
	Version             int       `gorm:"column:version;not null"`
	CreatedAt           time.Time `gorm:"column:created_at;not null"`
	CreatedBy           guid.ID   `gorm:"column:created_by;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null"`
	UpdatedBy           guid.ID   `gorm:"column:updated_by;not null"`
	ConnectionID        guid.ID   `gorm:"column:connection_id;not null"`
	ExternalProjectID   string    `gorm:"column:external_project_id;size:64;not null"`
	ExternalProjectName string    `gorm:"column:external_project_name;size:200;not null"`
	ProjectID           guid.ID   `gorm:"column:project_id;not null"`
	SyncMode            string    `gorm:"column:sync_mode;size:20;not null"`
	FieldMasters        string    `gorm:"column:field_masters;type:text;not null"`
	CodeupRepositories  string    `gorm:"column:codeup_repositories;type:text;not null"`
	TrustedPipelines    string    `gorm:"column:trusted_pipelines;type:text;not null"`
	Paused              bool      `gorm:"column:paused;not null"`
}

func (ProjectMapping) TableName() string { return "integration_project_mappings" }

type ExternalRef struct {
	ID                guid.ID    `gorm:"column:id;primaryKey"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	Version           int        `gorm:"column:version;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null"`
	UpdatedBy         guid.ID    `gorm:"column:updated_by;not null"`
	MappingID         guid.ID    `gorm:"column:mapping_id;not null"`
	ExternalKind      string     `gorm:"column:external_kind;size:32;not null"`
	ExternalID        string     `gorm:"column:external_id;size:64;not null"`
	LocalKind         string     `gorm:"column:local_kind;size:32;not null"`
	LocalID           guid.ID    `gorm:"column:local_id;not null"`
	ExternalUpdatedAt *time.Time `gorm:"column:external_updated_at"`
	LocalVersion      *int       `gorm:"column:local_version"`
	LastSyncedAt      *time.Time `gorm:"column:last_synced_at"`
}

func (ExternalRef) TableName() string { return "integration_external_refs" }

type StateMapping struct {
	ID                 guid.ID    `gorm:"column:id;primaryKey"`
	TenantID           guid.ID    `gorm:"column:tenant_id;not null"`
	Version            int        `gorm:"column:version;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	CreatedBy          guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
	UpdatedBy          guid.ID    `gorm:"column:updated_by;not null"`
	MappingID          guid.ID    `gorm:"column:mapping_id;not null"`
	ExternalType       string     `gorm:"column:external_type;size:80;not null"`
	ExternalStatusID   string     `gorm:"column:external_status_id;size:64;not null"`
	ExternalStatusName string     `gorm:"column:external_status_name;size:80;not null"`
	LocalWorkItemType  *string    `gorm:"column:local_work_item_type;size:24"`
	MapsToRequirement  bool       `gorm:"column:maps_to_requirement;not null"`
	LocalStateID       guid.ID    `gorm:"column:local_state_id;not null"`
	Confidence         int        `gorm:"column:confidence;not null"`
	ConfirmedBy        *guid.ID   `gorm:"column:confirmed_by"`
	ConfirmedAt        *time.Time `gorm:"column:confirmed_at"`
}

func (StateMapping) TableName() string { return "integration_state_mappings" }

type ImportJob struct {
	ID                 guid.ID    `gorm:"column:id;primaryKey"`
	TenantID           guid.ID    `gorm:"column:tenant_id;not null"`
	Version            int        `gorm:"column:version;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	CreatedBy          guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
	UpdatedBy          guid.ID    `gorm:"column:updated_by;not null"`
	MappingID          guid.ID    `gorm:"column:mapping_id;not null"`
	State              string     `gorm:"column:state;size:24;not null"`
	Scope              string     `gorm:"column:scope;type:text;not null"`
	DryRunReportFileID *guid.ID   `gorm:"column:dry_run_report_file_id"`
	Counts             string     `gorm:"column:counts;type:text;not null"`
	FailuresFileID     *guid.ID   `gorm:"column:failures_file_id"`
	StartedAt          *time.Time `gorm:"column:started_at"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
	JobID              *guid.ID   `gorm:"column:job_id"`
}

func (ImportJob) TableName() string { return "integration_import_jobs" }

type InboundEvent struct {
	ID            guid.ID    `gorm:"column:id;primaryKey"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	Version       int        `gorm:"column:version;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	CreatedBy     guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
	UpdatedBy     guid.ID    `gorm:"column:updated_by;not null"`
	ConnectionID  guid.ID    `gorm:"column:connection_id;not null"`
	Source        string     `gorm:"column:source;size:16;not null"`
	EventKind     string     `gorm:"column:event_kind;size:64;not null"`
	DeliveryKey   string     `gorm:"column:delivery_key;size:128;not null"`
	SignatureOK   bool       `gorm:"column:signature_ok;not null"`
	PayloadHash   string     `gorm:"column:payload_hash;size:64;not null"`
	PayloadFileID guid.ID    `gorm:"column:payload_file_id;not null"`
	State         string     `gorm:"column:state;size:16;not null"`
	Error         *string    `gorm:"column:error;size:500"`
	ProcessedAt   *time.Time `gorm:"column:processed_at"`
}

func (InboundEvent) TableName() string { return "integration_inbound_events" }

type SyncLog struct {
	ID           guid.ID   `gorm:"column:id;primaryKey"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	Version      int       `gorm:"column:version;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	CreatedBy    guid.ID   `gorm:"column:created_by;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
	UpdatedBy    guid.ID   `gorm:"column:updated_by;not null"`
	MappingID    guid.ID   `gorm:"column:mapping_id;not null"`
	Direction    string    `gorm:"column:direction;size:3;not null"`
	ExternalKind string    `gorm:"column:external_kind;size:32;not null"`
	ExternalID   string    `gorm:"column:external_id;size:64;not null"`
	LocalID      guid.ID   `gorm:"column:local_id;not null"`
	Field        *string   `gorm:"column:field;size:32"`
	Outcome      string    `gorm:"column:outcome;size:32;not null"`
	Detail       string    `gorm:"column:detail;type:text;not null"`
}

func (SyncLog) TableName() string { return "integration_sync_log" }
