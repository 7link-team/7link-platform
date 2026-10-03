package webhook

import (
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
)

const (
	SubscriptionActive   = "active"
	SubscriptionDisabled = "disabled"

	DeliveryPending    = "pending"
	DeliveryDelivering = "delivering"
	DeliveryDelivered  = "delivered"
	DeliveryFailed     = "failed"
)

var (
	ErrInvalidSubscription   = errors.New("invalid webhook subscription")
	ErrSubscriptionNotFound  = errors.New("webhook subscription not found")
	ErrSubscriptionNameTaken = errors.New("webhook subscription name exists")
	ErrDeliveryFailed        = errors.New("webhook delivery failed")
)

// Subscription is what a tenant registers and reads back. The signing secret is
// never returned: a caller is told whether one is set, which is all it needs to
// render a form, and rotating it is a write.
type Subscription struct {
	ID            guid.ID   `json:"id"`
	TenantID      guid.ID   `json:"tenant_id"`
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	SecretSet     bool      `json:"secret_set"`
	EventTypes    []string  `json:"event_types" doc:"Event types to deliver; empty means every type"`
	Status        string    `json:"status"`
	AfterSequence int64     `json:"after_sequence" doc:"The tenant audit sequence this subscription has been offered up to"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SubscriptionInput carries the validated fields the management API accepts.
type SubscriptionInput struct {
	Name       string
	URL        string
	Secret     string
	EventTypes []string
	Status     string
}

// matchesEventType reports whether a subscription wants an event type. An empty
// selection means every type, which is the shape a tenant that just wants
// everything would otherwise have to keep up to date by hand.
func matchesEventType(selection string, eventType string) bool {
	selection = strings.TrimSpace(selection)
	if selection == "" {
		return true
	}
	for _, wanted := range strings.Fields(selection) {
		if wanted == eventType {
			return true
		}
	}
	return false
}
