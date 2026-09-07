// Package model holds todo's persisted row type. It is a package of its own so
// the generated query code can reference the row without importing todo, which
// would close an import cycle back through the query package.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// TodoRow is one task. Version carries the optimistic lock; DeletedAt is the
// soft-delete predicate. The *By columns hold the trusted acting principal;
// historical rows can retain zero actors and unassigned data boundaries.
type TodoRow struct {
	ID           guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID     *guid.ID   `gorm:"column:tenant_id"`
	EntityID     *guid.ID   `gorm:"column:entity_id"`
	DepartmentID *guid.ID   `gorm:"column:department_id"`
	Title        string     `gorm:"column:title;size:255;not null"`
	Description  string     `gorm:"column:description"`
	Status       string     `gorm:"column:status;size:16;not null"`
	Priority     int        `gorm:"column:priority;not null"`
	DueAt        *time.Time `gorm:"column:due_at"`
	CompletedAt  *time.Time `gorm:"column:completed_at"`
	Version      int        `gorm:"column:version;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy    guid.ID    `gorm:"column:created_by;not null"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy    guid.ID    `gorm:"column:updated_by;not null"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
	DeletedBy    guid.ID    `gorm:"column:deleted_by;not null"`
}

func (TodoRow) TableName() string { return "todos" }
