// Package audit provides a transport-independent operation log. Every entry
// point — HTTP requests, gRPC calls, scheduled jobs, distributed tasks, and
// queue consumers — records who did what, to which resource, through which
// entry, and with what result.
//
// The package is deliberately transport-agnostic. Protocol adapters (the HTTP
// middleware and gRPC interceptors) and the generic wrapper audit.Do /
// Module.Operate all funnel into the same Recorder, which asynchronously
// persists events to a set of Sinks (structured logs always, the database when
// enabled). A new entry point — a cron job, a Kafka consumer, a WebSocket
// handler — records an operation by resolving the Recorder from the application
// service registry and calling either audit.Do (for a self-contained unit of
// work) or audit.Annotate (to enrich an operation that a middleware or
// interceptor already opened).
package audit

// EntryKind identifies the entry point that produced an event. The set is open:
// future transports may define new kinds without changing the core.
type EntryKind string

const (
	KindHTTP     EntryKind = "http"
	KindGRPC     EntryKind = "grpc"
	KindStream   EntryKind = "stream"
	KindJob      EntryKind = "job"
	KindSchedule EntryKind = "schedule"
	KindMessage  EntryKind = "message"
	KindInternal EntryKind = "internal"
)

// Access classifies an operation as read-only or write. Read operations are
// skipped by default; write operations are always recorded.
type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// Outcome is the terminal result of an operation.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// ActorType describes the kind of principal that performed the operation.
// The type is recorded so actor_id is unambiguous when a user and a service
// happen to share an identifier.
type ActorType string

const (
	ActorUser      ActorType = "user"
	ActorService   ActorType = "service"
	ActorSystem    ActorType = "system"
	ActorAnonymous ActorType = "anonymous"
)

// Omitted explains why a request or response body was not captured. The zero
// value means the body is present.
type Omitted string

const (
	OmittedNone               Omitted = ""
	OmittedUnsupportedContent Omitted = "unsupported_content_type"
	OmittedTooLarge           Omitted = "too_large"
	OmittedInvalidJSON        Omitted = "invalid_json"
	OmittedSuccessSummary     Omitted = "success_summary"
	OmittedStream             Omitted = "stream"
)
