package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

type correlationPublisher struct{}

func (correlationPublisher) Publish(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	b, err := json.Marshal(e.Data)
	if err != nil {
		return 0, err
	}
	return 1, tx.Exec("INSERT INTO trace_effects(kind,data) VALUES (?,?)", "outbox", string(b)).Error
}

func TestCorrelationFrozenIdentityAuditOutboxAndExport(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) })
	f := executionHarness(t)
	require.NoError(t, f.db.Exec("CREATE TABLE trace_effects(kind TEXT NOT NULL,data TEXT NOT NULL)").Error)
	f.m.events = correlationPublisher{}
	f.m.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		return tx.Exec("INSERT INTO trace_effects(kind,data) VALUES (?,?)", "audit", string(b)).Error
	}
	parent, parentSpan := provider.Tracer("http").Start(f.human, "request")
	f.human = parent
	in := validCreate()
	in.Plan.Correlation = &Correlation{TraceID: strings.Repeat("f", 32), SpanID: strings.Repeat("e", 16)}
	in.Plan.Objective = "PRIVATE OBJECTIVE MUST NOT BE EXPORTED"
	var created ExecutionView
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = f.m.CreateOn(f.human, tx, 7, in)
		return err
	}))
	c := created.Plan.Correlation
	require.NotNil(t, c)
	require.Equal(t, parentSpan.SpanContext().TraceID().String(), c.TraceID)
	require.Equal(t, parentSpan.SpanContext().SpanID().String(), c.ParentSpanID)
	require.NotEqual(t, in.Plan.Correlation.SpanID, c.SpanID)
	require.Equal(t, c, f.read(t, created.ID).Plan.Correlation)
	f.pull(t, created.ID)
	receipt := ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}
	_, err := f.receipt(created.ID, receipt)
	require.NoError(t, err)
	before := len(exporter.GetSpans())
	replayed, err := f.receipt(created.ID, receipt)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Len(t, exporter.GetSpans(), before, "replay must not fabricate a second command span")
	var stored []struct{ Kind, Data string }
	require.NoError(t, f.db.Table("trace_effects").Find(&stored).Error)
	require.Len(t, stored, 6)
	for i := 0; i < len(stored); i += 2 {
		var a, o map[string]any
		require.NoError(t, json.Unmarshal([]byte(stored[i].Data), &a))
		require.NoError(t, json.Unmarshal([]byte(stored[i+1].Data), &o))
		for _, k := range []string{"tenant_id", "project_id", "work_item_id", "execution_id", "command_id", "trace_id", "span_id", "parent_span_id", "runtime", "model"} {
			require.NotEmpty(t, a[k], k)
			require.Equal(t, a[k], o[k], k)
		}
		require.Equal(t, c.TraceID, a["trace_id"])
		require.Equal(t, c.SpanID, a["parent_span_id"])
	}
	for _, s := range exporter.GetSpans() {
		if s.Name != "execution.state_changed" {
			continue
		}
		require.Equal(t, c.TraceID, s.SpanContext.TraceID().String())
		require.Equal(t, c.SpanID, s.Parent.SpanID().String())
		b, err := json.Marshal(s.Attributes)
		require.NoError(t, err)
		require.NotContains(t, string(b), "PRIVATE")
		require.NotContains(t, string(b), "/registered/workspace")
	}
	parentSpan.End()
	// A command attempt may be traced, but transactional facts must roll back.
	f2 := executionHarness(t)
	require.NoError(t, f2.db.Exec("CREATE TABLE trace_effects(kind TEXT NOT NULL,data TEXT NOT NULL)").Error)
	f2.m.events = correlationPublisher{}
	f2.m.audit = f.m.audit
	rollback := errors.New("outer transaction rollback")
	require.ErrorIs(t, f2.db.Transaction(func(tx *gorm.DB) error {
		_, err := f2.m.CreateOn(f2.human, tx, 7, validCreate())
		if err != nil {
			return err
		}
		return rollback
	}), rollback)
	var n int64
	require.NoError(t, f2.db.Table("trace_effects").Count(&n).Error)
	require.Zero(t, n)
}

func TestCorrelationVerifierPlanUsesExecutionParentAndSafeVersion(t *testing.T) {
	f, request := verificationFixture(t)
	parent := f.read(t, request.ExecutionID).Plan.Correlation
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = f.m.queueVerificationOn(f.human, tx, request)
		return err
	}))
	q := query.BindVerificationTasks(f.db, f.m.names)
	r, err := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	var p VerificationPlan
	require.NoError(t, json.Unmarshal([]byte(r.PlanJSON), &p))
	require.NotNil(t, p.Correlation)
	require.Equal(t, parent.TraceID, p.Correlation.TraceID)
	require.Equal(t, parent.SpanID, p.Correlation.ParentSpanID)
	require.NotEqual(t, parent.SpanID, p.Correlation.SpanID)
	require.Equal(t, r.PlanHash, verificationPlanHash(p))
	row, err := query.BindExecutions(f.db, f.m.names).WithContext(t.Context()).First()
	require.NoError(t, err)
	p.TemplateVerifier = &TemplateVerifierPin{TemplateVersionID: 918, DefinitionHash: strings.Repeat("a", 64)}
	_, fields, span := traceEvent(withVerificationCorrelation(t.Context(), p), row, f.read(t, row.ID).Plan, "execution.verification_completed", map[string]any{"private": "not an exported attribute"})
	span.End()
	require.Equal(t, "918:"+strings.Repeat("a", 64), fields["verifier_version"])
	require.Equal(t, request.ResultID.String(), fields["result_id"])
	require.Equal(t, p.ID, fields["verifier_task_id"])
	require.Equal(t, request.ExecutionID.String(), fields["claim_id"])
}

func TestCorrelationHistoricalAndInvalidParents(t *testing.T) {
	c, err := newCorrelation(t.Context(), nil)
	require.NoError(t, err)
	tid, err := trace.TraceIDFromHex(c.TraceID)
	require.NoError(t, err)
	require.True(t, tid.IsValid())
	require.Empty(t, c.ParentSpanID)
	for _, bad := range []*Correlation{{}, {TraceID: c.TraceID, SpanID: strings.Repeat("0", 16)}, {TraceID: strings.Repeat("0", 32), SpanID: c.SpanID}} {
		_, err = newCorrelation(t.Context(), bad)
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestCorrelationActualOTLPReceiverRetainsVerifierAndRuntimeIdentity(t *testing.T) {
	received := make(chan *collector.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request collector.ExportTraceServiceRequest
		if r.URL.Path != "/v1/traces" || proto.Unmarshal(b, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- &request
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write([]byte{})
	}))
	defer server.Close()
	exporter, err := otlptracehttp.New(t.Context(), otlptracehttp.WithEndpoint(strings.TrimPrefix(server.URL, "http://")), otlptracehttp.WithInsecure(), otlptracehttp.WithTimeout(time.Second))
	require.NoError(t, err)
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() { otel.SetTracerProvider(old); require.NoError(t, provider.Shutdown(context.Background())) }()
	f, request := verificationFixture(t)
	// Discard the create span; this assertion concerns the verifier event.
	<-received
	row, err := query.BindExecutions(f.db, f.m.names).WithContext(t.Context()).First()
	require.NoError(t, err)
	c := f.read(t, row.ID).Plan.Correlation
	p := VerificationPlan{ID: "45", ResultID: "50", Correlation: c, ReviewerRuntime: "CLAUDE_CODE", ReviewerModel: "review-model", ReviewerFamily: "review-family", TemplateVerifier: &TemplateVerifierPin{TemplateVersionID: 92, DefinitionHash: strings.Repeat("b", 64)}}
	_, _, span := traceEvent(withVerificationCorrelation(t.Context(), p), row, f.read(t, row.ID).Plan, "execution.verification_completed", map[string]any{"body": "PRIVATE SECRET", "argv": request.Argv})
	span.End()
	select {
	case actual := <-received:
		require.Len(t, actual.ResourceSpans, 1)
		s := actual.ResourceSpans[0].ScopeSpans[0].Spans[0]
		require.Equal(t, "execution.verification_completed", s.Name)
		attrs := map[string]string{}
		for _, a := range s.Attributes {
			attrs[a.Key] = a.Value.GetStringValue()
		}
		require.Equal(t, "review-model", attrs["model"])
		require.Equal(t, "CLAUDE_CODE", attrs["runtime"])
		require.Equal(t, "92:"+strings.Repeat("b", 64), attrs["verifier_version"])
		require.Equal(t, row.ID.String(), attrs["execution_id"])
		require.Equal(t, c.TraceID, attrs["trace_id"])
		require.Empty(t, attrs["body"])
		require.Empty(t, attrs["argv"])
	case <-time.After(3 * time.Second):
		t.Fatal("actual OTLP receiver got no verifier span")
	}
}
