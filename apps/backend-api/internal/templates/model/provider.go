package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type TemplateProvider struct {
	TenantID          guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Kind              string     `gorm:"column:kind;not null"`
	AdapterKey        string     `gorm:"column:adapter_key;not null"`
	DisplayName       string     `gorm:"column:display_name;not null"`
	Config            string     `gorm:"column:config;not null"`
	SealedSecret      string     `gorm:"column:sealed_secret;not null" json:"-"`
	MachineID         guid.ID    `gorm:"column:machine_id;not null"`
	State             string     `gorm:"column:state;not null"`
	LicenseSHA256     string     `gorm:"column:license_sha256;not null"`
	LicenseAcceptedBy guid.ID    `gorm:"column:license_accepted_by;not null"`
	LicenseAcceptedAt time.Time  `gorm:"column:license_accepted_at;not null;autoCreateTime:false"`
	HealthCheckedAt   *time.Time `gorm:"column:health_checked_at"`
	HealthNote        string     `gorm:"column:health_note;not null"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null"`
	Version           int        `gorm:"column:version;not null"`
}

func (TemplateProvider) TableName() string { return "templates_providers" }

type TemplateProviderBinding struct {
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	BindingID       guid.ID   `gorm:"column:binding_id;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	Kind            string    `gorm:"column:kind;not null"`
	ProviderID      guid.ID   `gorm:"column:provider_id;not null"`
	ProviderVersion int       `gorm:"column:provider_version;not null"`
	CreatedBy       guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (TemplateProviderBinding) TableName() string { return "templates_provider_bindings" }

// ProviderGeneration retains its original encrypted credential and public
// configuration pin. Later provider edits cannot rewrite output provenance.
type ProviderGeneration struct {
	TenantID        guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID       guid.ID    `gorm:"column:project_id;not null"`
	BindingID       guid.ID    `gorm:"column:binding_id;not null"`
	ProviderID      guid.ID    `gorm:"column:provider_id;not null"`
	ProviderVersion int        `gorm:"column:provider_version;not null"`
	ExecutionID     guid.ID    `gorm:"column:execution_id;not null"`
	Kind            string     `gorm:"column:kind;not null"`
	AdapterKey      string     `gorm:"column:adapter_key;not null"`
	ConfigSnapshot  string     `gorm:"column:config_snapshot;not null"`
	SecretSnapshot  string     `gorm:"column:secret_snapshot;not null" json:"-"`
	MachineID       guid.ID    `gorm:"column:machine_id;not null"`
	Request         string     `gorm:"column:request;not null"`
	RequestSHA256   string     `gorm:"column:request_sha256;not null"`
	JobRef          string     `gorm:"column:job_ref;not null"`
	State           string     `gorm:"column:state;not null"`
	OutputSnapshot  string     `gorm:"column:output_snapshot;not null"`
	OutputSHA256    string     `gorm:"column:output_sha256;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CompletedAt     *time.Time `gorm:"column:completed_at"`
	Version         int        `gorm:"column:version;not null"`
}

func (ProviderGeneration) TableName() string { return "templates_provider_generations" }
