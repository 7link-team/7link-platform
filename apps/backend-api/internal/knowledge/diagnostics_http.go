package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type DiagnosticHistoryInput struct {
	KnowledgeTenantInput
	Scope            string `query:"scope" enum:"TEAM,PROJECT,AGENT" default:"TEAM"`
	ProjectID        string `query:"project_id"`
	AgentPrincipalID string `query:"agent_principal_id"`
	Search           string `query:"search" maxLength:"500"`
	Offset           int    `query:"offset" default:"0" minimum:"0"`
	Limit            int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type DiagnosticHistoryPage struct {
	Items  []RetrievalDebugResult `json:"items"`
	Offset int                    `json:"offset"`
	Limit  int                    `json:"limit"`
	Total  int64                  `json:"total"`
}
type KnowledgeMetricsInput struct {
	KnowledgeTenantInput
	From      time.Time `query:"from" required:"true"`
	To        time.Time `query:"to" required:"true"`
	ProjectID string    `query:"project_id"`
}
type MetricRate struct {
	Value       *float64 `json:"value,omitempty"`
	Numerator   int64    `json:"numerator"`
	Denominator int64    `json:"denominator"`
	Reason      string   `json:"reason,omitempty"`
}
type MetricLatency struct {
	P95MS   *float64 `json:"p95_ms,omitempty"`
	Samples int      `json:"samples"`
	Reason  string   `json:"reason,omitempty"`
}
type MemoryMetrics struct {
	Written   int64 `json:"written"`
	Escalated int64 `json:"escalated"`
	Approved  int64 `json:"approved"`
	Rejected  int64 `json:"rejected"`
	Revoked   int64 `json:"revoked"`
}
type KnowledgeMetrics struct {
	InjectionCoverage      string        `json:"injection_coverage" doc:"EXPLICIT_KNOWLEDGE_CONTEXT_ASSEMBLIES_ONLY excludes legacy failed and unrecorded automatic attempts; an empty execution plan is not a measured miss."`
	From                   time.Time     `json:"from"`
	To                     time.Time     `json:"to"`
	Injections             int64         `json:"injections"`
	HitRate                MetricRate    `json:"hit_rate"`
	UsedRate               MetricRate    `json:"used_rate"`
	CitationAcceptanceRate MetricRate    `json:"citation_acceptance_rate"`
	Searches               int64         `json:"searches"`
	SearchZeroResultRate   MetricRate    `json:"search_zero_result_rate"`
	SearchLatency          MetricLatency `json:"search_latency"`
	InjectionLatency       MetricLatency `json:"injection_latency"`
	DiagnosticLatency      MetricLatency `json:"diagnostic_latency"`
	DiagnosticSearches     int64         `json:"diagnostic_searches"`
	Memory                 MemoryMetrics `json:"memory"`
	Inclusions             int64         `json:"inclusions"`
	Citations              int64         `json:"citations"`
}

func metricRate(n, d int64, reason string) MetricRate {
	v := MetricRate{Numerator: n, Denominator: d}
	if d == 0 {
		v.Reason = reason
	} else {
		ratio := float64(n) / float64(d)
		v.Value = &ratio
	}
	return v
}
func metricLatency(samples []float64, reason string) MetricLatency {
	v := MetricLatency{Samples: len(samples)}
	if len(samples) == 0 {
		v.Reason = reason
		return v
	}
	slices.Sort(samples)
	index := (95*len(samples)+99)/100 - 1
	v.P95MS = &samples[index]
	return v
}

func (m *Module) historyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in DiagnosticHistoryInput) (DiagnosticHistoryPage, error) {
	out := DiagnosticHistoryPage{Items: []RetrievalDebugResult{}, Offset: in.Offset, Limit: in.Limit}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 || len([]rune(in.Search)) > 500 {
		return out, ErrInvalid
	}
	scope, err := listScope(knowledgeListInput{Scope: in.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID})
	if err != nil {
		return out, err
	}
	if err = m.authorizeDebugOn(ctx, tx, tenant, scope); err != nil {
		return out, err
	}
	read := query.BindDiagnosticLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "scope": scope.Scope, "project_id": scope.ProjectID, "agent_principal_id": scope.AgentPrincipalID})
	// Literal search is evaluated before paging; SQL LIKE wildcard semantics never change the filter.
	var rows []model.DiagnosticLog
	if err = read.Order("id DESC").Find(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		if !strings.Contains(strings.ToLower(row.QueryText), strings.ToLower(in.Search)) {
			continue
		}
		var saved RetrievalDebugResult
		if err = json.Unmarshal([]byte(row.ResultJSON), &saved); err != nil {
			return out, err
		}
		fresh, e := m.retrieveDebugOn(ctx, tx, tenant, saved.Query)
		if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrConflict) {
			continue
		}
		if e != nil {
			return out, e
		}
		if !sameDebugEntries(saved, fresh) {
			continue
		}
		out.Total++
		if out.Total > int64(in.Offset) && len(out.Items) < in.Limit {
			out.Items = append(out.Items, saved)
		}
	}
	return out, nil
}
func (m *Module) metricsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in KnowledgeMetricsInput) (KnowledgeMetrics, error) {
	out := KnowledgeMetrics{From: in.From, To: in.To, InjectionCoverage: "UNAVAILABLE"}
	if in.From.IsZero() || in.To.IsZero() || !in.To.After(in.From) || in.To.Sub(in.From) > 90*24*time.Hour {
		return out, ErrInvalid
	}
	scope := Scope{Scope: "TEAM"}
	if in.ProjectID != "" {
		id, err := guid.RequireID(in.ProjectID)
		if err != nil {
			return out, ErrInvalid
		}
		scope = Scope{Scope: "PROJECT", ProjectID: &id}
	}
	if _, err := m.authorize(ctx, tx, tenant, scope, "knowledge_read", false); err != nil {
		return out, err
	}
	// Resolve each entry's current scope before its receipts contribute to a total.
	q := query.BindEntries(tx, m.names)
	entries, err := q.WithContext(ctx).Select(q.ID, q.Scope, q.ProjectID, q.AgentPrincipalID, q.Kind, q.CreatedAt).Where(q.TenantID.Eq(tenant)).Find()
	if err != nil {
		return out, err
	}
	allowed := map[guid.ID]bool{}
	for _, row := range entries {
		if scope.ProjectID != nil && (row.ProjectID == nil || *row.ProjectID != *scope.ProjectID) {
			continue
		}
		_, e := m.authorize(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, "knowledge_read", false)
		if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		allowed[row.ID] = true
		if row.Kind != "DOCUMENT" && !row.CreatedAt.Before(in.From) && row.CreatedAt.Before(in.To) {
			out.Memory.Written++
		}
	}
	var receipts []model.UsageReceipt
	r := query.BindUsageReceipts(tx, m.names)
	if err = r.WithContext(ctx).Where(map[string]any{"tenant_id": tenant}).Where("created_at >= ? AND created_at < ?", in.From, in.To).Find(&receipts).Error; err != nil {
		return out, err
	}
	type pair struct {
		execution, entry guid.ID
		hash             string
	}
	included, cited := map[pair]bool{}, map[pair]bool{}
	for _, row := range receipts {
		if !allowed[row.EntryID] {
			continue
		}
		key := pair{row.ExecutionID, row.EntryID, row.ContentHash}
		if row.Kind == "INCLUSION" {
			included[key] = true
		} else if row.Kind == "CITATION" {
			cited[key] = true
		}
	}
	var used int64
	for key := range included {
		if cited[key] {
			used++
		}
	}
	out.Inclusions = int64(len(included))
	out.Citations = int64(len(cited))
	out.UsedRate = metricRate(used, out.Inclusions, "NO_VERIFIED_INCLUSION_SAMPLES")
	reviews := query.BindReviews(tx, m.names)
	reviewRows, err := reviews.WithContext(ctx).Where(reviews.TenantID.Eq(tenant), reviews.CreatedAt.Gte(in.From), reviews.CreatedAt.Lt(in.To)).Find()
	if err != nil {
		return out, err
	}
	for _, row := range reviewRows {
		if !allowed[row.EntryID] {
			continue
		}
		switch row.Action {
		case "ESCALATED":
			out.Memory.Escalated++
		case "APPROVED":
			out.Memory.Approved++
		case "REJECTED":
			out.Memory.Rejected++
		case "REVOKED":
			out.Memory.Revoked++
		}
	}
	var samples []model.RuntimeMetricSample
	sampleRead := query.BindRuntimeMetricSamples(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant}).Where("created_at >= ? AND created_at < ?", in.From, in.To)
	if scope.ProjectID != nil {
		sampleRead = sampleRead.Where(map[string]any{"project_id": *scope.ProjectID})
	}
	if err = sampleRead.Find(&samples).Error; err != nil {
		return out, err
	}
	// Source owners project genuine frozen historical facts, including zero-hit
	// requests. Unknown historical latency is kept absent.
	m.metrics.RLock()
	projection := m.metrics.projection.ReadOn
	m.metrics.RUnlock()
	if projection != nil {
		out.InjectionCoverage = "EXPLICIT_KNOWLEDGE_CONTEXT_ASSEMBLIES_ONLY"
		facts, e := projection(ctx, tx, tenant, scope.ProjectID, in.From, in.To)
		if e != nil {
			return out, e
		}
		type sampleKey struct {
			kind                       string
			execution, evidence, entry guid.ID
		}
		seen := map[sampleKey]bool{}
		for _, row := range samples {
			seen[sampleKey{row.Kind, metricIDValue(row.ExecutionID), row.EvidenceID, metricIDValue(row.EntryID)}] = true
		}
		for _, fact := range facts {
			key := sampleKey{fact.Kind, fact.ExecutionID, fact.EvidenceID, fact.EntryID}
			if seen[key] || fact.OccurredAt.Before(in.From) || !fact.OccurredAt.Before(in.To) {
				continue
			}
			if fact.Kind != "INJECTION" && fact.Kind != "CITATION_OUTCOME" {
				continue
			}
			if scope.ProjectID != nil && fact.ProjectID != *scope.ProjectID {
				continue
			}
			seen[key] = true
			var outcome *string
			if fact.Outcome != "" {
				outcome = &fact.Outcome
			}
			samples = append(samples, model.RuntimeMetricSample{ProjectID: nullableMetricID(fact.ProjectID), Kind: fact.Kind, ExecutionID: nullableMetricID(fact.ExecutionID), EvidenceID: fact.EvidenceID, EntryID: nullableMetricID(fact.EntryID), Hit: fact.Hit, LatencyMS: fact.LatencyMS, Outcome: outcome, CreatedAt: fact.OccurredAt})
		}
	}
	outcomeRows := map[[2]guid.ID]model.RuntimeMetricSample{}
	var hits, outcomes, accepted, zero int64
	latencies := []float64{}
	searchLatency := []float64{}
	for _, row := range samples {
		sampleScope := Scope{Scope: "TEAM"}
		if row.ProjectID != nil {
			sampleScope = Scope{Scope: "PROJECT", ProjectID: row.ProjectID}
		}
		if row.AgentPrincipalID != nil {
			sampleScope.Scope = "AGENT"
			sampleScope.AgentPrincipalID = row.AgentPrincipalID
		}
		_, e := m.authorize(ctx, tx, tenant, sampleScope, "knowledge_read", false)
		if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		if row.Kind == "INJECTION" {
			out.Injections++
			if row.Hit {
				hits++
			}
			if row.LatencyMS != nil {
				latencies = append(latencies, *row.LatencyMS)
			}
		} else if row.Kind == "SEARCH" {
			out.Searches++
			if !row.Hit {
				zero++
			}
			if row.LatencyMS != nil {
				searchLatency = append(searchLatency, *row.LatencyMS)
			}
		} else if row.Kind == "CITATION_OUTCOME" && allowed[metricIDValue(row.EntryID)] && row.Outcome != nil && (*row.Outcome == "ACCEPTED" || *row.Outcome == "REJECTED") {
			// Decisions can happen after the citation's reporting window. Require
			// the canonical verified receipt without an artificial date cutoff.
			var citations int64
			if err := query.BindUsageReceipts(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "entry_id": row.EntryID, "execution_id": row.ExecutionID, "kind": "CITATION"}).Count(&citations).Error; err != nil {
				return out, err
			}
			if citations == 0 {
				continue
			}
			key := [2]guid.ID{metricIDValue(row.ExecutionID), metricIDValue(row.EntryID)}
			previous, exists := outcomeRows[key]
			if !exists || row.CreatedAt.After(previous.CreatedAt) || row.CreatedAt.Equal(previous.CreatedAt) && row.EvidenceID > previous.EvidenceID {
				outcomeRows[key] = row
			}
		}
	}
	for _, row := range outcomeRows {
		outcomes++
		if row.Outcome != nil && *row.Outcome == "ACCEPTED" {
			accepted++
		}
	}
	out.HitRate = metricRate(hits, out.Injections, "NO_VERIFIED_INJECTION_REQUEST_SAMPLES")
	out.CitationAcceptanceRate = metricRate(accepted, outcomes, "NO_VERIFIED_CITATION_OUTCOMES")
	out.InjectionLatency = metricLatency(latencies, "NO_MEASURED_INJECTION_LATENCY")
	// Diagnostics never supply the manual-search denominator.
	out.SearchZeroResultRate = metricRate(zero, out.Searches, "NO_MANUAL_SEARCH_SAMPLES")
	out.SearchLatency = metricLatency(searchLatency, "NO_MEASURED_SEARCH_LATENCY")
	var logs []model.DiagnosticLog
	logRead := query.BindDiagnosticLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant}).Where("created_at >= ? AND created_at < ?", in.From, in.To)
	if scope.ProjectID != nil {
		logRead = logRead.Where(map[string]any{"project_id": *scope.ProjectID})
	}
	if err = logRead.Find(&logs).Error; err != nil {
		return out, err
	}
	debugLatency := []float64{}
	for _, row := range logs {
		if e := m.authorizeDebugOn(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}); e != nil {
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
				continue
			}
			return out, e
		}
		debugLatency = append(debugLatency, row.LatencyMS)
	}
	out.DiagnosticSearches = int64(len(debugLatency))
	out.DiagnosticLatency = metricLatency(debugLatency, "NO_DIAGNOSTIC_SAMPLES")
	return out, nil
}
func (m *Module) registerDiagnosticsHTTP(api huma.API) {
	m.registerKnowledgeSearchHTTP(api)
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("knowledge.retrieval.history", "/knowledge/retrieval-debug/history", "List currently authorized immutable retrieval diagnostic snapshots"), authz.Guard{Resource: "knowledge", Verb: "manage"}, func(ctx context.Context, in *DiagnosticHistoryInput) (*respx.Body[DiagnosticHistoryPage], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		var out DiagnosticHistoryPage
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.historyOn(ctx, tx, tenant, *in); return e })
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, op("knowledge.metrics", "/knowledge-metrics", "Read evidence-backed knowledge quality metrics in a maximum ninety day window"), authz.Guard{Resource: "knowledge", Verb: "read"}, func(ctx context.Context, in *KnowledgeMetricsInput) (*respx.Body[KnowledgeMetrics], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		var out KnowledgeMetrics
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.metricsOn(ctx, tx, tenant, *in); return e })
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}

// Virtual fact IDs use value types; only applicable IDs are represented in a
// persisted sample. Normalized values are used solely for in-memory map keys.
func nullableMetricID(id guid.ID) *guid.ID {
	if id == 0 {
		return nil
	}
	return &id
}
func metricIDValue(id *guid.ID) guid.ID {
	if id == nil {
		return 0
	}
	return *id
}
