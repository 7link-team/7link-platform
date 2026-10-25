package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// EmbeddingConfig holds sealed credentials; plaintext never crosses a read API.
type EmbeddingConfig struct {
	TenantID            guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	BaseURL             string     `gorm:"column:base_url;not null;size:512"`
	Model               string     `gorm:"column:model;not null;size:128"`
	Dims                int        `gorm:"column:dims;not null"`
	APIKeySealed        string     `gorm:"column:api_key_sealed;not null"`
	BatchSize           int        `gorm:"column:batch_size;not null"`
	RPM                 int        `gorm:"column:rpm;not null"`
	TPM                 int        `gorm:"column:tpm;not null"`
	TimeoutMS           int        `gorm:"column:timeout_ms;not null"`
	State               string     `gorm:"column:state;not null;size:16"`
	ActiveProviderJSON  string     `gorm:"column:active_provider_json;not null"`
	ActiveModelKey      string     `gorm:"column:active_model_key;not null;size:64"`
	PendingModelKey     string     `gorm:"column:pending_model_key;not null;size:64"`
	ConsecutiveFailures int        `gorm:"column:consecutive_failures;not null"`
	DegradedUntil       *time.Time `gorm:"column:degraded_until"`
	CoveragePPM         int        `gorm:"column:coverage_ppm;not null"`
	Version             int        `gorm:"column:version;not null"`
	UpdatedBy           guid.ID    `gorm:"column:updated_by;not null"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (EmbeddingConfig) TableName() string { return "knowledge_embedding_configs" }
