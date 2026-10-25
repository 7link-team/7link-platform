package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// SearchLog records the immutable requested filter and result provenance.
// Content is reauthorized before a saved result is exposed.
type SearchLog struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ActorID     guid.ID   `gorm:"column:actor_id;not null"`
	Purpose     string    `gorm:"column:purpose;not null;size:16"`
	RequestJSON string    `gorm:"column:request_json;not null"`
	ResultJSON  string    `gorm:"column:result_json;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (SearchLog) TableName() string { return "knowledge_search_logs" }
