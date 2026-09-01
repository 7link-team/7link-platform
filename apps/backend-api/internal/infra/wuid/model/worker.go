// Package model holds wuid's persisted row types. It is a package of its own so
// the generated query code can reference the row type without importing wuid,
// which would close an import cycle back through the query package.
package model

// WorkerRow is one leased worker ID. Token identifies the holder, ExpiresAt is
// the database-clock millisecond at which the lease lapses, and the remaining
// fields record which machine took it, for operator diagnosis.
type WorkerRow struct {
	ID        int    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Token     string `gorm:"column:token;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
	OS        string `gorm:"column:os"`
	Host      string `gorm:"column:host"`
	IPv4Lan   string `gorm:"column:ipv4_lan"`
	MAC       string `gorm:"column:mac"`
	Disk      string `gorm:"column:disk"`
	Container bool   `gorm:"column:container"`
	KVM       bool   `gorm:"column:kvm"`
}

func (WorkerRow) TableName() string { return "worker_ids" }
