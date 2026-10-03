// Package model holds webhook's persisted rows.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// Subscription is one tenant's standing request to be told about its own events.
// AfterSequence is the cursor into that tenant's audit chain: the sequence this
// subscription has already been offered every event up to. Keeping it on the
// subscription rather than in a side table is what makes "deliver each event
// once" a single row update in the same transaction that enqueues the work.
type Subscription struct {
	ID               guid.ID `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID `gorm:"column:tenant_id;not null"`
	Name             string  `gorm:"column:name;size:128;not null"`
	URL              string  `gorm:"column:url;size:2048;not null"`
	SecretCiphertext string  `gorm:"column:secret_ciphertext;type:text;not null"`
	// EventTypes is a space-separated list, or empty for every event type. The
	// shape follows federation's scopes column: a bounded list a tenant edits as
	// one value, not a child table nothing else would ever join to.
	EventTypes    string    `gorm:"column:event_types;size:1024;not null"`
	Status        string    `gorm:"column:status;size:16;not null"`
	AfterSequence int64     `gorm:"column:after_sequence;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (Subscription) TableName() string { return "iam_webhook_subscriptions" }

// Delivery is one event owed to one subscription. It carries the event's
// identifiers rather than a rendered body, so a payload is built at send time
// from the current signing secret and never sits in storage waiting to be
// replayed.
type Delivery struct {
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	SubscriptionID guid.ID    `gorm:"column:subscription_id;not null"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null"`
	EventID        guid.ID    `gorm:"column:event_id;not null"`
	EventSequence  int64      `gorm:"column:event_sequence;not null"`
	EventType      string     `gorm:"column:event_type;size:64;not null"`
	Status         string     `gorm:"column:status;size:16;not null"`
	Attempts       int        `gorm:"column:attempts;not null"`
	AvailableAt    time.Time  `gorm:"column:available_at;not null"`
	LockedAt       *time.Time `gorm:"column:locked_at"`
	DeliveredAt    *time.Time `gorm:"column:delivered_at"`
	LastError      string     `gorm:"column:last_error;size:512;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (Delivery) TableName() string { return "iam_webhook_deliveries" }
