// Package events owns the transactional domain-event outbox.
package events

//go:generate go run ./dbgen
import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
)

const Name = "events"

var (
	ErrUnavailable      = errors.New("events: publisher unavailable")
	ErrInvalidEvent     = errors.New("events: invalid event")
	ErrUnknownEventType = errors.New("events: undeclared event type")
	ErrCatalogSealed    = errors.New("events: catalog sealed")
)

type Actor struct {
	Kind            string
	ID              guid.ID
	AuthoritySource string
}
type Subject struct {
	Kind string
	ID   guid.ID
}
type Event struct {
	TenantID, ProjectID guid.ID
	Type                string
	Actor               Actor
	Subject             Subject
	SubjectVersion      int64
	Data                any
	ExtraTopics         []string
	CorrelationID       string
	CausationID         guid.ID
}
type RealtimeMode string

const (
	RealtimeNone   RealtimeMode = "NONE"
	RealtimeSignal RealtimeMode = "SIGNAL"
	RealtimeFull   RealtimeMode = "FULL"
)

type TypeSpec struct {
	Name string
	// Module identifies the declaring module; it defaults to the registration owner.
	Module         string
	DisplayNameKey string
	DescriptionKey string
	SchemaVersion  int
	SubjectKind    string
	Realtime       RealtimeMode
}

// Publisher must participate in the caller's active transaction. Any error must
// abort that transaction. Successful publication does not mean delivery.
type Publisher interface {
	Publish(context.Context, *gorm.DB, Event) (guid.ID, error)
}

// Catalog receives declarations during dependency injection, before Initialize.
type Catalog interface {
	AddTypes(owner string, specs ...TypeSpec) error
}

// EphemeralPublisher transports realtime hints without domain subscriber delivery.
type EphemeralPublisher func(context.Context, *gorm.DB, Event) (guid.ID, error)
