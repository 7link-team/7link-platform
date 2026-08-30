package audit

import (
	"context"
	"encoding/json"
	"log/slog"
)

// SlogSink writes audit events to the structured logger. It is the always-on
// fallback: even when the database sink is disabled or unavailable, events are
// still persisted to the platform log pipeline (ELK/Loki), including their
// redacted bodies.
type SlogSink struct{}

func NewSlogSink() *SlogSink { return &SlogSink{} }

func (sink *SlogSink) Write(ctx context.Context, events []Event) error {
	for _, event := range events {
		slog.LogAttrs(ctx, slog.LevelInfo, "operation",
			slog.String("audit.id", event.ID),
			slog.Time("audit.occurred_at", event.OccurredAt),
			slog.Float64("audit.duration_ms", event.DurationMS),
			slog.String("audit.entry", string(event.Entry)),
			slog.String("audit.operation", event.Operation),
			slog.String("audit.access", string(event.Access)),
			slog.String("audit.action", event.Action),
			slog.String("audit.resource_type", event.ResourceType),
			slog.String("audit.resource_id", event.ResourceID),
			slog.String("audit.actor_id", event.Actor.ID),
			slog.String("audit.actor_type", string(event.Actor.Type)),
			slog.String("audit.actor_name", event.Actor.Name),
			slog.String("audit.tenant_id", event.Actor.TenantID),
			slog.String("audit.client_ip", event.Client.IP),
			slog.String("audit.user_agent", event.Client.UserAgent),
			slog.String("audit.request_id", event.Client.RequestID),
			slog.String("audit.session_id", event.Client.SessionID),
			slog.String("audit.host", event.Client.Host),
			slog.String("audit.target_method", event.Target.Method),
			slog.String("audit.target_route", event.Target.Route),
			slog.String("audit.outcome", string(event.Outcome)),
			slog.Int("audit.status_code", event.StatusCode),
			slog.Int("audit.code", event.Code),
			slog.String("audit.message", event.Message),
			slog.String("audit.error", event.Error),
			slog.Int64("audit.request_size", event.Request.Size),
			slog.Bool("audit.request_truncated", event.Request.Truncated),
			slog.String("audit.request_omitted", string(event.Request.Omitted)),
			slog.String("audit.request_body", event.Request.Body),
			slog.Int64("audit.response_size", event.Response.Size),
			slog.Bool("audit.response_truncated", event.Response.Truncated),
			slog.String("audit.response_omitted", string(event.Response.Omitted)),
			slog.String("audit.response_body", event.Response.Body),
			slog.String("audit.trace_id", event.TraceID),
			slog.String("audit.span_id", event.SpanID),
			slog.String("audit.service", event.Service),
			slog.String("audit.version", event.Version),
			slog.String("audit.instance", event.Instance),
			slog.String("audit.metadata", encodeMetadata(event.Metadata)),
		)
	}
	return nil
}

func encodeMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(raw)
}
