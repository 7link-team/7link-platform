package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ManualModelCatalog preserves Human entries independently of runner observations.
type ManualModelCatalog struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	MachineID    guid.ID   `gorm:"column:machine_id;not null"`
	Runtime      string    `gorm:"column:runtime;size:32;not null"`
	ModelsJSON   string    `gorm:"column:models_json;not null"`
	FamiliesJSON string    `gorm:"column:families_json;not null"`
	Version      int       `gorm:"column:version;not null;default:1"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (ManualModelCatalog) TableName() string { return "fleet_manual_model_catalogs" }
