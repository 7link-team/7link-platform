// Package audit owns the IAM domain-event contract.
package audit

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// DomainEvent describes one IAM state change written in the same transaction
// as that change.
type DomainEvent struct {
	TenantID    guid.ID
	PrincipalID guid.ID
	EventType   string
	TargetType  string
	TargetID    guid.ID
	Detail      map[string]any
}

// Appender writes an event through db. Callers pass their active transaction
// so the state change and its audit event commit or roll back together.
type Appender func(ctx context.Context, db *gorm.DB, event DomainEvent) error

// NewDomainEvent initializes an event and copies the verified principal from
// the authentication context when one is present.
func NewDomainEvent(
	ctx context.Context,
	tenantID guid.ID,
	eventType string,
	targetType string,
	targetID guid.ID,
) DomainEvent {
	return DomainEvent{
		TenantID:    tenantID,
		PrincipalID: authn.PrincipalIDFromContext(ctx),
		EventType:   eventType,
		TargetType:  targetType,
		TargetID:    targetID,
		Detail:      map[string]any{},
	}
}
