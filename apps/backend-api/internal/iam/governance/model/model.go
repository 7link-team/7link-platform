// Package model contains governance's persisted row types.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type AccessRequest struct {
	TenantID         guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	RequesterID      guid.ID    `gorm:"column:requester_id;not null"`
	RoleID           guid.ID    `gorm:"column:role_id;not null"`
	RoleName         string     `gorm:"column:role_name;size:128;not null"`
	Reason           string     `gorm:"column:reason;type:text;not null"`
	SnapshotJSON     string     `gorm:"column:snapshot_json;type:text;not null"`
	Status           string     `gorm:"column:status;size:16;not null"`
	AccessExpiresAt  time.Time  `gorm:"column:access_expires_at;not null"`
	RequestExpiresAt time.Time  `gorm:"column:request_expires_at;not null"`
	DecidedBy        *guid.ID   `gorm:"column:decided_by"`
	DecisionNote     string     `gorm:"column:decision_note;type:text;not null"`
	DecidedAt        *time.Time `gorm:"column:decided_at"`
	RevokedBy        *guid.ID   `gorm:"column:revoked_by"`
	RevokeReason     string     `gorm:"column:revoke_reason;type:text;not null"`
	RevokedAt        *time.Time `gorm:"column:revoked_at"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null"`
}

func (AccessRequest) TableName() string { return "iam_access_requests" }

type ApprovalStep struct {
	TenantID  guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	RequestID guid.ID    `gorm:"column:request_id;primaryKey;autoIncrement:false"`
	Step      int        `gorm:"column:step;primaryKey;autoIncrement:false"`
	Decision  string     `gorm:"column:decision;size:16;not null"`
	DecidedBy *guid.ID   `gorm:"column:decided_by"`
	DecidedAt *time.Time `gorm:"column:decided_at"`
	Note      string     `gorm:"column:note;type:text;not null"`
}

func (ApprovalStep) TableName() string { return "iam_approval_steps" }

type AccessReview struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Name        string     `gorm:"column:name;size:128;not null"`
	OwnerID     guid.ID    `gorm:"column:owner_id;not null"`
	Status      string     `gorm:"column:status;size:16;not null"`
	DueAt       time.Time  `gorm:"column:due_at;not null"`
	CompletedAt *time.Time `gorm:"column:completed_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
}

func (AccessReview) TableName() string { return "iam_access_reviews" }

type AccessReviewItem struct {
	TenantID       guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ReviewID       guid.ID    `gorm:"column:review_id;primaryKey;autoIncrement:false"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID    guid.ID    `gorm:"column:principal_id;not null"`
	PrincipalName  string     `gorm:"column:principal_name;size:255;not null"`
	RoleID         guid.ID    `gorm:"column:role_id;not null"`
	RoleName       string     `gorm:"column:role_name;size:128;not null"`
	GrantType      string     `gorm:"column:grant_type;size:16;not null"`
	GrantID        *guid.ID   `gorm:"column:grant_id"`
	GrantCreatedAt time.Time  `gorm:"column:grant_created_at;not null"`
	GrantExpiresAt *time.Time `gorm:"column:grant_expires_at"`
	Decision       string     `gorm:"column:decision;size:16;not null"`
	DecidedBy      *guid.ID   `gorm:"column:decided_by"`
	DecisionNote   string     `gorm:"column:decision_note;type:text;not null"`
	DecidedAt      *time.Time `gorm:"column:decided_at"`
}

func (AccessReviewItem) TableName() string { return "iam_access_review_items" }
