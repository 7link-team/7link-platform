package knowledge

import (
	"context"
	"net/http"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type KnowledgeSearchInput struct {
	DiagnosticHistoryInput
	Query string `query:"query" required:"true" minLength:"1" maxLength:"500"`
	Mode  string `query:"mode" enum:"ALL,ANY" default:"ALL"`
}
type KnowledgeSearchPage struct {
	Items     []RetrievalDebugCandidate `json:"items"`
	Offset    int                       `json:"offset"`
	Limit     int                       `json:"limit"`
	Total     int                       `json:"total"`
	Truncated bool                      `json:"truncated"`
	Engine    string                    `json:"engine"`
}

func (m *Module) searchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in KnowledgeSearchInput) (KnowledgeSearchPage, error) {
	out := KnowledgeSearchPage{Items: []RetrievalDebugCandidate{}, Offset: in.Offset, Limit: in.Limit, Engine: "LEXICAL_ENTRIES"}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return out, ErrInvalid
	}
	scope, err := listScope(knowledgeListInput{Scope: in.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID})
	if err != nil {
		return out, err
	}
	start := time.Now()
	result, err := m.retrieveLexicalOn(ctx, tx, tenant, RetrievalDebugQuery{Scope: scope, Query: in.Query, Mode: in.Mode, TopK: 200}, false)
	if err != nil {
		return out, err
	}
	matches := []RetrievalDebugCandidate{}
	for _, candidate := range result.Candidates {
		if candidate.Rank > 0 {
			matches = append(matches, candidate)
		}
	}
	out.Total = len(matches)
	out.Truncated = result.Truncated || len(result.Candidates) == 200
	if in.Offset < len(matches) {
		end := in.Offset + in.Limit
		if end > len(matches) {
			end = len(matches)
		}
		out.Items = matches[in.Offset:end]
	}
	{
		id, e := m.nextSourceID()
		if e != nil {
			return out, e
		}
		ms := elapsedMS(start)
		row := model.RuntimeMetricSample{ID: id, TenantID: tenant, ProjectID: scope.ProjectID, AgentPrincipalID: scope.AgentPrincipalID, Kind: "SEARCH", EvidenceID: id, Hit: len(matches) > 0, LatencyMS: &ms, CreatedAt: m.now()}
		if e = query.BindRuntimeMetricSamples(tx, m.names).WithContext(ctx).Create(&row).Error; e != nil {
			return out, e
		}
	}
	return out, nil
}
func (m *Module) registerKnowledgeSearchHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.search", Method: http.MethodGet, Path: "/knowledge/search", Summary: "Search authorized lexical knowledge and record actual search observations", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, func(ctx context.Context, in *KnowledgeSearchInput) (*respx.Body[KnowledgeSearchPage], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		var out KnowledgeSearchPage
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.searchOn(ctx, tx, tenant, *in); return e })
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
