package query

import (
	"7link.com/platform/internal/iam/audit/model"
	"gorm.io/gorm"
)

type (
	Events            = event
	Heads             = head
	RetentionPolicies = retentionPolicy
)

type Tables struct {
	Events            *Events
	Heads             *Heads
	RetentionPolicies *RetentionPolicies
}

// Bind applies prefix to every table owned by the audit module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Events:            q.Event.Table(prefix + model.Event{}.TableName()),
		Heads:             q.Head.Table(prefix + model.Head{}.TableName()),
		RetentionPolicies: q.RetentionPolicy.Table(prefix + model.RetentionPolicy{}.TableName()),
	}
}
