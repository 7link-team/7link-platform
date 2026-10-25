package execution

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"maps"
	"strconv"

	"7link.com/platform/internal/execution/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Correlation is frozen by the server, never accepted as runner evidence. An
// absent value on historical plans means that no trace was recorded.
type Correlation struct {
	TraceID      string `json:"trace_id" readOnly:"true"`
	SpanID       string `json:"span_id" readOnly:"true"`
	ParentSpanID string `json:"parent_span_id,omitempty" readOnly:"true"`
}

func newCorrelation(ctx context.Context, parent *Correlation) (*Correlation, error) {
	var tid trace.TraceID
	var sid trace.SpanID
	parentID := ""
	if parent != nil {
		var err error
		tid, err = trace.TraceIDFromHex(parent.TraceID)
		if err != nil || !tid.IsValid() {
			return nil, ErrUnavailable
		}
		ps, err := trace.SpanIDFromHex(parent.SpanID)
		if err != nil || !ps.IsValid() {
			return nil, ErrUnavailable
		}
		parentID = ps.String()
	} else if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		tid, parentID = sc.TraceID(), sc.SpanID().String()
	}
	for !tid.IsValid() {
		if _, err := rand.Read(tid[:]); err != nil {
			return nil, err
		}
	}
	for !sid.IsValid() {
		if _, err := rand.Read(sid[:]); err != nil {
			return nil, err
		}
	}
	return &Correlation{TraceID: tid.String(), SpanID: sid.String(), ParentSpanID: parentID}, nil
}

type verificationCorrelationKey struct{}

type verificationCorrelation struct {
	Plan VerificationPlan
}

func withVerificationCorrelation(ctx context.Context, p VerificationPlan) context.Context {
	return context.WithValue(ctx, verificationCorrelationKey{}, verificationCorrelation{Plan: p})
}

// traceEvent exports a command-attempt span and returns the exact same safe
// identifiers for the transaction's audit and outbox. The span does not assert
// that its caller's transaction committed. Bodies, argv, paths and errors are
// deliberately excluded from telemetry.
func traceEvent(ctx context.Context, row *model.Execution, p ExecutionPlan, kind string, detail map[string]any) (context.Context, map[string]any, trace.Span) {
	c := p.Correlation
	fields := maps.Clone(detail)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["tenant_id"], fields["project_id"] = row.TenantID.String(), row.ProjectID.String()
	fields["work_item_id"], fields["execution_id"] = row.WorkItemID.String(), row.ID.String()
	fields["command_id"] = row.ID.String() + ":" + strconv.FormatInt(row.DispatchEpoch, 10)
	fields["runtime"], fields["model"], fields["model_family"] = p.Runtime, p.Model, p.ModelFamily
	if row.State == "SUCCEEDED" && row.BranchReason != "ENSEMBLE" && row.BranchReason != "SKILL_DISTILLATION" && row.BranchReason != "HANDOVER" {
		fields["claim_id"] = row.ID.String()
	}
	if v, ok := ctx.Value(verificationCorrelationKey{}).(verificationCorrelation); ok {
		c = v.Plan.Correlation
		fields["verifier_task_id"], fields["result_id"] = v.Plan.ID, v.Plan.ResultID
		fields["criterion_id"], fields["verification_stage"] = v.Plan.CriterionID, v.Plan.Stage
		fields["verifier_plan_hash"] = verificationPlanHash(v.Plan)
		if pin := v.Plan.TemplateVerifier; pin != nil {
			fields["verifier_version"] = pin.TemplateVersionID.String() + ":" + pin.DefinitionHash
		}
		fields["runtime"], fields["model"], fields["model_family"] = v.Plan.ReviewerRuntime, v.Plan.ReviewerModel, v.Plan.ReviewerFamily
	}
	if c != nil {
		tid, te := trace.TraceIDFromHex(c.TraceID)
		sid, se := trace.SpanIDFromHex(c.SpanID)
		if te == nil && se == nil && tid.IsValid() && sid.IsValid() {
			ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
			fields["trace_id"], fields["span_id"] = c.TraceID, c.SpanID
			if c.ParentSpanID != "" {
				fields["parent_span_id"] = c.ParentSpanID
			}
		}
	}
	ctx, span := otel.Tracer("execution").Start(ctx, kind)
	if sc := span.SpanContext(); sc.IsValid() {
		if parent, ok := fields["span_id"].(string); ok && parent != sc.SpanID().String() {
			fields["parent_span_id"] = parent
		}
		fields["trace_id"], fields["span_id"] = sc.TraceID().String(), sc.SpanID().String()
	}
	attrs := []attribute.KeyValue{attribute.String("transaction.outcome", "uncommitted")}
	// Only the fixed allowlist is exported; arbitrary event detail is not.
	for _, key := range []string{"tenant_id", "project_id", "work_item_id", "execution_id", "command_id", "claim_id", "runtime", "model", "model_family", "verifier_task_id", "result_id", "criterion_id", "verification_stage", "verifier_version", "verifier_plan_hash", "trace_id", "span_id", "parent_span_id"} {
		if value, ok := fields[key].(string); ok && value != "" {
			attrs = append(attrs, attribute.String(key, value))
		}
	}
	span.SetAttributes(attrs...)
	logs := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		logs = append(logs, slog.String(string(a.Key), a.Value.AsString()))
	}
	slog.LogAttrs(ctx, slog.LevelDebug, "Execution domain command attempt", logs...)
	return ctx, fields, span
}

// Used in audit/export correlation without disclosing a verifier's private plan.
func verificationPlanHash(p VerificationPlan) string {
	encoded, _ := json.Marshal(p)
	return hash(encoded)
}

func traceID(detail map[string]any) string {
	value, _ := detail["trace_id"].(string)
	return value
}
