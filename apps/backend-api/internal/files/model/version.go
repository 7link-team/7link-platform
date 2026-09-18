package model

import "7link.com/platform/internal/infra/guid"

// Version records immutable content and original upload attributes. Renaming a
// node must not rewrite these attributes or the shared physical object's type.
type Version struct {
	ID              guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	SpaceID         guid.ID  `gorm:"column:space_id"`
	NodeID          guid.ID  `gorm:"column:node_id"`
	NodeKind        string   `gorm:"column:node_kind"`
	Number          int64    `gorm:"column:version_number"`
	ObjectID        guid.ID  `gorm:"column:object_id"`
	Ext             string   `gorm:"column:ext"`
	DeclaredMIME    string   `gorm:"column:declared_mime"`
	CreatedBy       guid.ID  `gorm:"column:created_by"`
	CreatedAt       int64    `gorm:"column:created_at;autoCreateTime:false"`
	SourceVersionID *guid.ID `gorm:"column:source_version_id"`
}

func (Version) TableName() string { return "file_versions" }
