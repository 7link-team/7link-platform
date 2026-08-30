package audit

import "time"

// Event is one audited operation. The JSON shape mirrors the operation_logs
// table so events can be round-tripped through either sink unchanged.
type Event struct {
	// ID is the unique event identifier: the platform snowflake ID as a decimal
	// string, matching the guid.ID JSON convention and the BIGINT primary key.
	ID string `json:"id"`
	// OccurredAt is when the operation started, in UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// DurationMS is the wall-clock duration of the operation.
	DurationMS float64 `json:"duration_ms"`

	// Entry names the transport that produced the event.
	Entry EntryKind `json:"entry"`
	// Operation is the human-readable operation identity, e.g. "POST /orders".
	Operation string `json:"operation"`
	// Access is read or write.
	Access Access `json:"access"`
	// Action is an optional domain verb, e.g. "order.cancel". Enrich via
	// audit.Action in business code.
	Action string `json:"action,omitempty"`

	// ResourceType and ResourceID identify the affected resource, e.g.
	// ("order", "1042"). Enrich via audit.Resource.
	ResourceType string `json:"resource_type,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`

	// Actor is the principal that performed the operation.
	Actor Actor `json:"actor,omitempty"`
	// Client describes the calling client (peer IP, user agent, request ID).
	Client Client `json:"client,omitempty"`
	// Target describes the server-side route and method.
	Target Target `json:"target,omitempty"`

	// Outcome is success or failure.
	Outcome Outcome `json:"outcome"`
	// StatusCode is the HTTP status code or numeric gRPC status code.
	StatusCode int `json:"status_code,omitempty"`
	// Code is the response envelope code (0 on success) when available.
	Code int `json:"code,omitempty"`
	// Message is the response envelope message on success.
	Message string `json:"message,omitempty"`
	// Error is the human-readable failure detail.
	Error string `json:"error,omitempty"`

	// Request and Response carry the captured, redacted payloads.
	Request  Payload `json:"request,omitempty"`
	Response Payload `json:"response,omitempty"`

	// TraceID and SpanID tie the event to its OpenTelemetry trace.
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`

	// Service, Version, and Instance identify the process that recorded the event.
	Service  string `json:"service,omitempty"`
	Version  string `json:"version,omitempty"`
	Instance string `json:"instance,omitempty"`

	// Metadata carries free-form key/value context added by business code.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Actor identifies the principal that performed an operation.
type Actor struct {
	ID       string    `json:"id,omitempty"`
	Type     ActorType `json:"type,omitempty"`
	Name     string    `json:"name,omitempty"`
	TenantID string    `json:"tenant_id,omitempty"`
}

// Client describes the calling client. Headers are never recorded wholesale;
// only the whitelisted fields below are captured, and Authorization/Cookie are
// never persisted.
type Client struct {
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Host      string `json:"host,omitempty"`
}

// Target identifies the server-side operation target.
type Target struct {
	Method string `json:"method,omitempty"`
	Route  string `json:"route,omitempty"`
}

// Payload is a captured and redacted request or response body. Body is empty
// when Omitted is set.
type Payload struct {
	Body      string  `json:"body,omitempty"`
	Size      int64   `json:"size,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Omitted   Omitted `json:"omitted,omitempty"`
}
