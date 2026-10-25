package knowledge

import (
	"context"
	"net/http"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type MemoryAgentInput struct {
	KnowledgeTenantInput
	AgentPrincipalID string `path:"agent_principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type MemoryListInput struct {
	MemoryAgentInput
	ProjectID string `query:"project_id"`
	Kind      string `query:"kind" enum:"EXPERIENCE,RULE"`
	State     string `query:"state" enum:"ACTIVE,PENDING_REVIEW,REJECTED,REVOKED,EXPIRED,SUPERSEDED"`
	Search    string `query:"search" maxLength:"200"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset    int    `query:"offset" default:"0" minimum:"0"`
}
type MemoryStats struct {
	ActiveExperience int `json:"active_experience"`
	ActiveRules      int `json:"active_rules"`
	Pending          int `json:"pending"`
	TodayWrites      int `json:"today_writes"`
	TodayRules       int `json:"today_rules"`
	ExperienceLimit  int `json:"experience_limit"`
	RuleLimit        int `json:"rule_limit"`
	DailyLimit       int `json:"daily_limit"`
	DailyRuleLimit   int `json:"daily_rule_limit"`
	PendingLimit     int `json:"pending_limit"`
}
type MemoryPage struct {
	Items  []EntryView `json:"items"`
	Total  int         `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
	Stats  MemoryStats `json:"stats"`
}
type MemoryIDInput struct {
	KnowledgeTenantInput
	MemoryID string `path:"memory_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type MemoryReviewListInput struct {
	MemoryIDInput
	Search string `query:"search" maxLength:"200"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
}
type MemoryHistoryView struct {
	ID        guid.ID   `json:"id"`
	Action    string    `json:"action"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Reason    string    `json:"reason"`
	ActorID   guid.ID   `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
}
type MemoryHistoryPage struct {
	Items  []MemoryHistoryView `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}
type memoryEditHTTPInput struct {
	MemoryIDInput
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           MemoryMutation
}
type memoryPromoteHTTPInput struct {
	MemoryIDInput
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           MemoryPromotion
}

func (m *Module) registerMemoryHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{401, 403, 404, 409, 422, 429, 500, 503}}
	}
	authz.Register(m.registrar, api, op("knowledge.memory.list", http.MethodGet, "/agents/{agent_principal_id}/memories", "List Agent memories and current quota statistics", 200), authz.Guard{Resource: "knowledge", Verb: "read"}, m.memoryListHTTP)
	authz.Register(m.registrar, api, op("knowledge.memory.history", http.MethodGet, "/memories/{memory_id}/reviews", "List immutable memory lifecycle history", 200), authz.Guard{Resource: "knowledge", Verb: "read"}, m.memoryHistoryHTTP)
	authz.Register(m.registrar, api, op("knowledge.memory.edit", http.MethodPost, "/memories/{memory_id}/edits", "Replace a memory with a superseding entry", 201), authz.Guard{Resource: "knowledge", Verb: "write"}, m.memoryEditHTTP)
	authz.Register(m.registrar, api, op("knowledge.memory.promote", http.MethodPost, "/memories/{memory_id}/promotions", "Promote an Agent rule into a broader review scope", 201), authz.Guard{Resource: "memory", Verb: "approve"}, m.memoryPromoteHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	for _, path := range []string{"/agents/{agent_principal_id}/memories", "/memories/{memory_id}/reviews", "/memories/{memory_id}/edits", "/memories/{memory_id}/promotions"} {
		if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) memoryListOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in MemoryListInput) (MemoryPage, error) {
	out := MemoryPage{Items: []EntryView{}, Limit: in.Limit, Offset: in.Offset, Stats: MemoryStats{ExperienceLimit: 500, RuleLimit: 100, DailyLimit: 20, DailyRuleLimit: 5, PendingLimit: 20}}
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 || len(in.Search) > 800 {
		return out, ErrInvalid
	}
	scope := Scope{Scope: "AGENT", AgentPrincipalID: &agent}
	if in.ProjectID != "" {
		project, err := guid.RequireID(in.ProjectID)
		if err != nil {
			return out, ErrInvalid
		}
		scope.ProjectID = &project
	}
	readerKind, err := m.authorize(ctx, tx, tenant, scope, "knowledge_read", false)
	if err != nil {
		return out, err
	}
	if readerKind == "HUMAN" {
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: "AGENT", AgentPrincipalID: &agent}, "agent_read", true); err != nil {
			return out, err
		}
	}
	q := query.BindEntries(tx, m.names)
	base := func() query.IEntryDo {
		return q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq("AGENT"), q.AgentPrincipalID.Eq(agent), q.Kind.In("EXPERIENCE", "RULE"))
	}
	count, err := base().Where(q.State.Eq("ACTIVE"), q.Kind.Eq("EXPERIENCE")).Count()
	if err != nil {
		return out, err
	}
	out.Stats.ActiveExperience = int(count)
	count, err = base().Where(q.State.Eq("ACTIVE"), q.Kind.Eq("RULE")).Count()
	if err != nil {
		return out, err
	}
	out.Stats.ActiveRules = int(count)
	count, err = base().Where(q.State.Eq("PENDING_REVIEW")).Count()
	if err != nil {
		return out, err
	}
	out.Stats.Pending = int(count)
	midnight := m.now().UTC().Truncate(24 * time.Hour)
	count, err = base().Where(q.CreatedBy.Eq(agent), q.CreatedAt.Gte(midnight)).Count()
	if err != nil {
		return out, err
	}
	out.Stats.TodayWrites = int(count)
	count, err = base().Where(q.CreatedBy.Eq(agent), q.CreatedAt.Gte(midnight), q.Kind.Eq("RULE")).Count()
	if err != nil {
		return out, err
	}
	out.Stats.TodayRules = int(count)
	read := base()
	if scope.ProjectID == nil {
		read = read.Where(q.ProjectID.IsNull())
	} else {
		read = read.Where(q.ProjectID.Eq(*scope.ProjectID))
	}
	if in.Kind != "" {
		read = read.Where(q.Kind.Eq(in.Kind))
	}
	if in.State != "" {
		read = read.Where(q.State.Eq(in.State))
	}
	if in.Search != "" {
		search := "%" + in.Search + "%"
		read = read.Where(q.WithContext(ctx).Where(q.Title.Like(search)).Or(q.Body.Like(search)))
	}
	count, err = read.Count()
	if err != nil {
		return out, err
	}
	out.Total = int(count)
	rows, err := read.Order(q.CreatedAt.Desc(), q.ID.Desc()).Limit(in.Limit).Offset(in.Offset).Find()
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		view := entryView(row)
		view.ConflictMemoryIDs, err = m.ruleConflictsOn(ctx, tx, row)
		if err != nil {
			return out, err
		}
		if len(view.ConflictMemoryIDs) > 0 && !slices.Contains(view.RiskFlags, "CONFLICT") {
			view.RiskFlags = append(view.RiskFlags, "CONFLICT")
		}
		out.Items = append(out.Items, view)
	}
	return out, nil
}
func (m *Module) memoryListHTTP(ctx context.Context, in *MemoryListInput) (*respx.Body[MemoryPage], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	agent, err := guid.RequireID(in.AgentPrincipalID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	var out MemoryPage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.memoryListOn(ctx, tx, tenant, agent, *in); return e })
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) memoryHistoryHTTP(ctx context.Context, in *MemoryReviewListInput) (*respx.Body[MemoryHistoryPage], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.MemoryID)
	if err != nil || in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	out := MemoryHistoryPage{Items: []MemoryHistoryView{}, Limit: in.Limit, Offset: in.Offset}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, e := m.memoryEntryOn(ctx, tx, tenant, id, "knowledge_read", false); e != nil {
			return e
		}
		q := query.BindReviews(tx, m.names)
		read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.EntryID.Eq(id))
		if in.Search != "" {
			term := "%" + in.Search + "%"
			read = read.Where(q.WithContext(ctx).Where(q.Action.Like(term)).Or(q.Reason.Like(term)))
		}
		count, e := read.Count()
		if e != nil {
			return e
		}
		out.Total = int(count)
		rows, e := read.Order(q.ID.Desc()).Offset(in.Offset).Limit(in.Limit).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Items = append(out.Items, MemoryHistoryView{ID: r.ID, Action: r.Action, FromState: r.FromState, ToState: r.ToState, Reason: r.Reason, ActorID: r.ActorID, CreatedAt: r.CreatedAt})
		}
		return nil
	})
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) memoryEditHTTP(ctx context.Context, in *memoryEditHTTPInput) (*knowledgeCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.MemoryID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	in.Body.memoryID = id
	return m.command(ctx, tenant, in.IdempotencyKey, "knowledge.memory.edit", "/memories/{memory_id}/edits", map[string]string{"tenant_id": in.TenantID, "memory_id": in.MemoryID}, in.Body, func(tx *gorm.DB) (EntryView, error) { return m.editMemoryOn(ctx, tx, tenant, id, in.Body) })
}
func (m *Module) memoryPromoteHTTP(ctx context.Context, in *memoryPromoteHTTPInput) (*knowledgeCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.MemoryID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return m.command(ctx, tenant, in.IdempotencyKey, "knowledge.memory.promote", "/memories/{memory_id}/promotions", map[string]string{"tenant_id": in.TenantID, "memory_id": in.MemoryID}, in.Body, func(tx *gorm.DB) (EntryView, error) { return m.promoteMemoryOn(ctx, tx, tenant, id, in.Body) })
}
