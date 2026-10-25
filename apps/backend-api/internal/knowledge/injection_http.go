package knowledge

import (
	"context"
	"encoding/json"
	"errors"
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

type InjectionHTTPInput struct {
	KnowledgeTenantInput
	ExecutionID string `path:"execution_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type ResultKnowledgeHTTPInput struct {
	KnowledgeTenantInput
	ResultID string `path:"result_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type UsedKnowledgeView struct {
	Feedback        []FeedbackContextItem `json:"feedback,omitempty"`
	ExecutionBudget int64                 `json:"execution_budget,omitempty"`
	ScoresAvailable bool                  `json:"scores_available"`
	Available       bool                  `json:"available"`
	Reason          string                `json:"reason,omitempty"`
	Retrieval       *InjectionResult      `json:"retrieval,omitempty"`
}
type usedKnowledgeHTTPOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[UsedKnowledgeView]
}

func (m *Module) registerInjectionHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.result.used", Method: http.MethodGet, Path: "/knowledge/results/{result_id}", Summary: "Read source-verified knowledge for a reviewed delivery result", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, m.resultKnowledgeHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.execution.used", Method: http.MethodGet, Path: "/knowledge/executions/{execution_id}", Summary: "Read the authorized frozen execution knowledge and verified usage", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, m.usedKnowledgeHTTP)
}
func (m *Module) resultKnowledgeHTTP(ctx context.Context, in *ResultKnowledgeHTTPInput) (*usedKnowledgeHTTPOutput, error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ResultID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	out := UsedKnowledgeView{Reason: "RESULT_HAS_NO_SOURCE_EXECUTION"}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		m.injection.RLock()
		read := m.injection.results.ReadOn
		m.injection.RUnlock()
		if read == nil {
			return ErrUnavailable
		}
		execution, err := read(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if execution == 0 {
			return nil
		}
		out, err = m.usedKnowledgeOn(ctx, tx, tenant, execution)
		return err
	})
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return &usedKnowledgeHTTPOutput{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) usedKnowledgeHTTP(ctx context.Context, in *InjectionHTTPInput) (*usedKnowledgeHTTPOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	execution, e := guid.RequireID(in.ExecutionID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, e := m.database()
	if e != nil {
		return nil, knowledgeError(ctx, e)
	}
	var out UsedKnowledgeView
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.usedKnowledgeOn(ctx, tx, tenant, execution)
		return err
	})
	if e != nil {
		return nil, knowledgeError(ctx, e)
	}
	return &usedKnowledgeHTTPOutput{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) usedKnowledgeOn(ctx context.Context, tx *gorm.DB, tenant, execution guid.ID) (UsedKnowledgeView, error) {
	out := UsedKnowledgeView{Reason: "NO_RECORDED_AUTOMATIC_RETRIEVAL"}
	snapshot, viewErr := m.executionKnowledgeViewOn(ctx, tx, tenant, execution)
	if viewErr != nil {
		return out, viewErr
	}
	out.Feedback, out.ExecutionBudget = snapshot.Feedback, snapshot.Budget
	var row model.InjectionLog
	err := query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "execution_id": execution}).Order("id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// An authorized legacy execution may still have frozen context. Without a
		// source reader, require TEAM authority before revealing log absence.
		scope := Scope{Scope: "TEAM"}
		if snapshot.ProjectID > 0 {
			scope = Scope{Scope: "PROJECT", ProjectID: &snapshot.ProjectID}
		}
		if _, err = m.authorize(ctx, tx, tenant, scope, "knowledge_read", false); err != nil {
			return out, err
		}
		if len(snapshot.Items) > 0 || len(snapshot.Feedback) > 0 {
			out.Available = true
			out.Reason = ""
			out.Retrieval = &InjectionResult{Mode: "SNAPSHOT_ONLY", Rules: []InjectionItem{}, Items: []InjectionItem{}}
			for _, item := range snapshot.Items {
				q := query.BindEntries(tx, m.names)
				r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.EntryID)).First()
				if e != nil {
					return out, e
				}
				if _, e = m.authorize(ctx, tx, tenant, Scope{Scope: r.Scope, ProjectID: r.ProjectID, AgentPrincipalID: r.AgentPrincipalID}, "knowledge_read", false); e != nil {
					return out, e
				}
				v := InjectionItem{ContextItem: item, Scope: r.Scope, SourceKind: "MEMORY", Tokens: tokenCost(item.Body), Inclusion: "PENDING", SourceChanged: r.Version != item.Version || r.ContentHash != item.ContentHash}
				if len(item.Label) > 2 && item.Label[1] == 'R' {
					out.Retrieval.Rules = append(out.Retrieval.Rules, v)
				} else {
					out.Retrieval.Items = append(out.Retrieval.Items, v)
				}
			}
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: "PROJECT", ProjectID: &row.ProjectID}, "knowledge_read", false); err != nil {
		return out, err
	}
	var result InjectionResult
	if err = json.Unmarshal([]byte(row.ResultJSON), &result); err != nil {
		return out, err
	}
	items := append(append([]InjectionItem{}, result.Rules...), result.Items...)
	contexts := make([]ContextItem, len(items))
	for i, item := range items {
		contexts[i] = item.ContextItem
	}
	if err = m.authorizeSnapshotOn(ctx, tx, tenant, row.ProjectID, contexts); err != nil {
		return out, err
	}
	for i, item := range items {
		q := query.BindEntries(tx, m.names)
		r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.EntryID)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// The shared retained-snapshot policy checks the exact frozen bytes
			// and conservatively preserves global/project Agent permissions.
			if err = m.authorizeSnapshotOn(ctx, tx, tenant, row.ProjectID, []ContextItem{item.ContextItem}); err != nil {
				return out, err
			}
			items[i].SourceChanged = true
			continue
		}
		if err != nil {
			return out, err
		}
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: r.Scope, ProjectID: r.ProjectID, AgentPrincipalID: r.AgentPrincipalID}, "knowledge_read", false); err != nil {
			return out, err
		}
		items[i].SourceChanged = r.Version != item.Version || r.ContentHash != item.ContentHash || !m.memoryActive(r)
	}
	copy(result.Rules, items[:len(result.Rules)])
	copy(result.Items, items[len(result.Rules):])
	m.metrics.RLock()
	projection := m.metrics.projection.ReadOn
	m.metrics.RUnlock()
	if projection != nil {
		facts, err := projection(ctx, tx, tenant, &row.ProjectID, row.CreatedAt.Add(-time.Second), m.now().Add(time.Millisecond))
		if err != nil {
			return out, err
		}
		apply := func(items []InjectionItem) {
			for i := range items {
				if !items[i].Cited {
					continue
				}
				for _, fact := range facts {
					if fact.Kind == "CITATION_OUTCOME" && fact.ExecutionID == execution && fact.EntryID == items[i].EntryID && (fact.Outcome == "ACCEPTED" || fact.Outcome == "REJECTED") {
						items[i].CitationOutcome = fact.Outcome
					}
				}
			}
		}
		apply(result.Rules)
		apply(result.Items)
	}
	out.ScoresAvailable = true
	out.Available = true
	out.Reason = ""
	out.Retrieval = &result
	return out, nil
}
