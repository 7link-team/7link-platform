// Package model holds dlock's persisted row types. It is a package of its own
// so the generated query code can reference the row type without importing
// dlock, which would close an import cycle back through the query package.
package model

// LockRow is one named lease: an owner token and the wall-clock millisecond,
// as read from the database, at which the lease expires.
type LockRow struct {
	Name      string `gorm:"column:name;primaryKey;size:191"`
	Owner     string `gorm:"column:owner;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
}

func (LockRow) TableName() string { return "dlocks" }
