package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// PackageSend records actual transport outcomes, independently of publication.
type PackageSend struct {
	TenantID       guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID      guid.ID   `gorm:"column:project_id;not null"`
	PackageID      guid.ID   `gorm:"column:package_id;not null"`
	PackageVersion int       `gorm:"column:package_version;not null"`
	ContentHash    string    `gorm:"column:content_hash;not null;size:64"`
	RecipientID    guid.ID   `gorm:"column:recipient_id;not null"`
	ChannelID      guid.ID   `gorm:"column:channel_id;not null"`
	Attempt        int       `gorm:"column:attempt_no;not null"`
	State          string    `gorm:"column:state;not null;size:16"`
	MessageID      *guid.ID  `gorm:"column:message_id"`
	FailureCode    *string   `gorm:"column:failure_code;size:64"`
	ActorID        guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (PackageSend) TableName() string { return "clientwork_package_sends" }
