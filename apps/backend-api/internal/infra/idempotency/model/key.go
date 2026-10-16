package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type KeyRow struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	SubjectKind  string    `gorm:"column:subject_kind;size:16;not null"`
	SubjectID    guid.ID   `gorm:"column:subject_id;not null"`
	Operation    string    `gorm:"column:operation;size:128;not null"`
	Key          string    `gorm:"column:key;size:64;not null"`
	RequestHash  string    `gorm:"column:request_hash;size:64;not null"`
	State        string    `gorm:"column:state;size:16;not null"`
	StatusCode   *int      `gorm:"column:status_code"`
	ResponseBody *string   `gorm:"column:response_body"`
	ResourceKind string    `gorm:"column:resource_kind;size:32;not null"`
	ResourceID   *guid.ID  `gorm:"column:resource_id"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	ExpiresAt    time.Time `gorm:"column:expires_at;not null"`
}

func (KeyRow) TableName() string { return "platform_idempotency_keys" }
