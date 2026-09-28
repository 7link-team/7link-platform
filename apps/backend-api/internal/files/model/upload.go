package model

import "7link.com/platform/internal/infra/guid"

// Upload binds identity, whole-file expectations, confirmed progress and expiry.
type Upload struct {
	ID             guid.ID  `gorm:"column:id;primaryKey;autoIncrement:false"`
	PrincipalID    guid.ID  `gorm:"column:principal_id"`
	TenantID       *guid.ID `gorm:"column:tenant_id"`
	BackendID      string   `gorm:"column:backend_id"`
	StorageKey     string   `gorm:"column:storage_key"`
	StorageHandle  string   `gorm:"column:storage_handle"`
	Name           string   `gorm:"column:name"`
	Size           int64    `gorm:"column:size"`
	Offset         int64    `gorm:"column:committed_offset"`
	MD5            string   `gorm:"column:expected_md5"`
	SHA1           string   `gorm:"column:expected_sha1"`
	SHA256         string   `gorm:"column:expected_sha256"`
	SHA512         string   `gorm:"column:expected_sha512"`
	ChecksumState  string   `gorm:"column:checksum_state"`
	State          string   `gorm:"column:state"`
	ReleaseState   string   `gorm:"column:release_state"`
	ReleaseError   string   `gorm:"column:release_error"`
	LastProgressAt int64    `gorm:"column:last_progress_at"`
	ExpiresAt      int64    `gorm:"column:expires_at"`
	ExpiredAt      *int64   `gorm:"column:expired_at"`
	LeaseUntil     int64    `gorm:"column:lease_until"`
	Fence          int64    `gorm:"column:fence"`
	Version        int64    `gorm:"column:version"`
	CreatedAt      int64    `gorm:"column:created_at;autoCreateTime:false"`
}

func (Upload) TableName() string { return "file_uploads" }

// UploadPart records a confirmed final-backend write at a sequential offset.
type UploadPart struct {
	ID           guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	UploadID     guid.ID `gorm:"column:upload_id"`
	Index        int     `gorm:"column:part_index"`
	Offset       int64   `gorm:"column:start_offset"`
	Length       int64   `gorm:"column:byte_length"`
	StorageToken string  `gorm:"column:storage_token"`
	State        string  `gorm:"column:state"`
	ReleaseState string  `gorm:"column:release_state"`
	CreatedAt    int64   `gorm:"column:created_at;autoCreateTime:false"`
}

func (UploadPart) TableName() string { return "file_upload_parts" }
