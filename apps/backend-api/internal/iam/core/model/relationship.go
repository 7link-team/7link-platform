package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

type Relationship struct {
	TenantID        guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	SubjectType     string     `gorm:"column:subject_type;primaryKey;size:16"`
	SubjectID       guid.ID    `gorm:"column:subject_id;primaryKey;autoIncrement:false"`
	SubjectRelation string     `gorm:"column:subject_relation;primaryKey;size:16"`
	Relation        string     `gorm:"column:relation;primaryKey;size:16"`
	ResourceType    string     `gorm:"column:resource_type;primaryKey;size:64"`
	ResourceID      guid.ID    `gorm:"column:resource_id;primaryKey;autoIncrement:false"`
	StartsAt        *time.Time `gorm:"column:starts_at"`
	EndsAt          *time.Time `gorm:"column:ends_at"`
	ConditionJSON   string     `gorm:"column:condition_json;type:text;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
}

func (Relationship) TableName() string { return "iam_relationships" }

type ResourceRelationship struct {
	TenantID        guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	EntityID        guid.ID    `gorm:"column:entity_id;primaryKey;autoIncrement:false"`
	SubjectType     string     `gorm:"column:subject_type;primaryKey;size:16"`
	SubjectID       guid.ID    `gorm:"column:subject_id;primaryKey;autoIncrement:false"`
	SubjectRelation string     `gorm:"column:subject_relation;primaryKey;size:16"`
	Relation        string     `gorm:"column:relation;primaryKey;size:16"`
	ResourceType    string     `gorm:"column:resource_type;primaryKey;size:64"`
	ResourceID      guid.ID    `gorm:"column:resource_id;primaryKey;autoIncrement:false"`
	StartsAt        *time.Time `gorm:"column:starts_at"`
	EndsAt          *time.Time `gorm:"column:ends_at"`
	ConditionJSON   string     `gorm:"column:condition_json;type:text;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
}

func (ResourceRelationship) TableName() string { return "iam_resource_relationships" }
