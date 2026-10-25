package knowledge

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type RetrievalTimings struct {
	TokenizationMS  float64  `json:"tokenization_ms"`
	AuthorizationMS float64  `json:"authorization_ms"`
	CandidatesMS    float64  `json:"candidates_ms"`
	RankingMS       float64  `json:"ranking_ms"`
	TotalMS         float64  `json:"total_ms"`
	VectorMS        *float64 `json:"vector_ms,omitempty" doc:"Absent when no vector stage executed."`
}
type RetrievalPlan struct {
	Mode               string `json:"mode"`
	Expression         string `json:"expression"`
	NativeSQLAvailable bool   `json:"native_sql_available"`
	NativeSQLReason    string `json:"native_sql_reason"`
	VectorAvailable    bool   `json:"vector_available"`
	VectorReason       string `json:"vector_reason"`
}

func lexicalRetrievalPlan(mode string) RetrievalPlan {
	return RetrievalPlan{Mode: "LEXICAL_ENTRIES", Expression: "authorized exact scope -> newest 1000 entries -> active BM25 terms (" + mode + ") -> candidate_limit -> T/R/W/S -> thresholds -> top_k", NativeSQLReason: "NATIVE_FTS_ADAPTER_NOT_INSTALLED", VectorReason: "VECTOR_ADAPTER_NOT_INSTALLED"}
}
func elapsedMS(start time.Time) float64 { return float64(time.Since(start).Nanoseconds()) / 1e6 }

func (m *Module) authorizeDebugPerspectiveOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RetrievalDebugQuery) error {
	if err := m.authorizeDebugOn(ctx, tx, tenant, in.Scope); err != nil {
		return err
	}
	if in.SimulateAgentID == nil {
		return nil
	}
	if *in.SimulateAgentID <= 0 || in.ProjectID == nil {
		return ErrInvalid
	}
	if m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, *in.SimulateAgentID)
	if err != nil {
		return err
	}
	if !found || agent.PrincipalID != *in.SimulateAgentID || agent.TenantID != tenant || agent.State != "ACTIVE" {
		return ErrForbidden
	}
	simulated := authn.WithClaims(ctx, &authn.Claims{PrincipalID: *in.SimulateAgentID, TenantID: tenant, SubjectType: authn.SubjectTypePrincipal})
	// Both the requesting Human and simulated Agent must retain current authority.
	_, err = m.authorize(simulated, tx, tenant, in.Scope, "knowledge_read", false)
	return err
}
func (m *Module) persistDiagnosticOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, result *RetrievalDebugResult) error {
	id, err := m.nextSourceID()
	if err != nil {
		return err
	}
	result.LogID = id
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	row := model.DiagnosticLog{ID: id, TenantID: tenant, ActorID: result.ActorID, Scope: result.Query.Scope.Scope, ProjectID: result.Query.ProjectID, AgentPrincipalID: result.Query.AgentPrincipalID, QueryText: result.Query.Query, ResultJSON: string(body), LatencyMS: result.Timings.TotalMS, CreatedAt: m.now()}
	return query.BindDiagnosticLogs(tx, m.names).WithContext(ctx).Create(&row).Error
}

// RuntimeMetricFact is a frozen runtime fact, never a client-reported metric.
type RuntimeMetricFact struct {
	Kind        string
	ProjectID   guid.ID
	ExecutionID guid.ID
	EvidenceID  guid.ID
	EntryID     guid.ID
	Hit         bool
	LatencyMS   *float64
	Outcome     string
	OccurredAt  time.Time
}
type RuntimeMetricProjection struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]RuntimeMetricFact, error)
}
type RuntimeMetricProjectionConnector struct {
	Bind func(RuntimeMetricProjection) error
}

type runtimeMetricBinding struct {
	sync.RWMutex
	projection RuntimeMetricProjection
}

func (m *Module) provideDiagnostics(s *module.Services) error {
	return module.Provide[RuntimeMetricProjectionConnector](s, RuntimeMetricProjectionConnector{Bind: func(p RuntimeMetricProjection) error {
		if p.ReadOn == nil {
			return ErrInvalid
		}
		m.metrics.Lock()
		defer m.metrics.Unlock()
		if m.metrics.projection.ReadOn != nil {
			return ErrConflict
		}
		m.metrics.projection = p
		return nil
	}})
}
