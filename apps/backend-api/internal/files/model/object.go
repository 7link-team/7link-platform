// Package model contains rows owned by the file module.
package model

import "7link.com/platform/internal/infra/guid"

// Object is one immutable object in one configured final storage backend.
type Object struct {
	ID               guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	BackendID        string  `gorm:"column:backend_id"`
	StorageKey       string  `gorm:"column:storage_key"`
	URI              string  `gorm:"column:uri"`
	State            string  `gorm:"column:state"`
	Size             int64   `gorm:"column:size"`
	MD5              string  `gorm:"column:md5"`
	SHA1             string  `gorm:"column:sha1"`
	SHA256           string  `gorm:"column:sha256"`
	SHA512           string  `gorm:"column:sha512"`
	MIME             string  `gorm:"column:mime_type"`
	Type             string  `gorm:"column:file_type"`
	Subtype          string  `gorm:"column:file_subtype"`
	Format           string  `gorm:"column:format"`
	Width            *int    `gorm:"column:width"`
	Height           *int    `gorm:"column:height"`
	MetadataJSON     string  `gorm:"column:metadata_json"`
	MetadataState    string  `gorm:"column:metadata_state"`
	ExtractorVersion string  `gorm:"column:extractor_version"`
	Version          int64   `gorm:"column:version"`
	CreatedAt        int64   `gorm:"column:created_at;autoCreateTime:false"`
}

func (Object) TableName() string { return "file_objects" }

// AccountReference holds private account resources independently of tenants.
// A pending reference protects an object until the account transaction resolves.
type AccountReference struct {
	ID          guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID `gorm:"column:principal_id"`
	ObjectID    guid.ID `gorm:"column:object_id"`
	Purpose     string  `gorm:"column:purpose"`
	State       string  `gorm:"column:state"`
	ExpiresAt   *int64  `gorm:"column:expires_at"`
	CreatedAt   int64   `gorm:"column:created_at;autoCreateTime:false"`
}

func (AccountReference) TableName() string { return "file_account_references" }

// AccountPreparation durably owns a generated key before storage I/O starts.
// Abandoned writes remain tombstones until their writer acknowledges cleanup.
type AccountPreparation struct {
	ID          guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID guid.ID `gorm:"column:principal_id"`
	BackendID   string  `gorm:"column:backend_id"`
	StorageKey  string  `gorm:"column:storage_key"`
	State       string  `gorm:"column:state"`
	ExpiresAt   int64   `gorm:"column:expires_at"`
	CreatedAt   int64   `gorm:"column:created_at;autoCreateTime:false"`
}

func (AccountPreparation) TableName() string { return "file_account_preparations" }
