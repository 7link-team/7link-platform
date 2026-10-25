package knowledge

import (
	"context"
	"net/http"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

func ruleNegative(text string) bool {
	text = strings.ToLower(text)
	count := 0
	// Longest phrases first prevent double counting "do not" as "not".
	for _, term := range []string{"不要", "禁止", "切勿", "never", "don't", "do not", "must not", "不"} {
		count += strings.Count(text, term)
		text = strings.ReplaceAll(text, term, " ")
	}
	return count%2 != 0
}

func conflictingRules(a, b string) bool {
	if ruleNegative(a) == ruleNegative(b) {
		return false
	}
	left, right := map[string]bool{}, map[string]bool{}
	for _, term := range lexicalTerms(a) {
		left[term] = true
	}
	for _, term := range lexicalTerms(b) {
		right[term] = true
	}
	common := 0
	for term := range left {
		if right[term] {
			common++
		}
	}
	union := len(left) + len(right) - common
	return union > 0 && common*2 >= union
}

// Re-evaluate current ACTIVE rules at approval time. The displayed queue hints
// never substitute for this transactional gate. Results contain IDs, not text
// from other memory scopes.
func (m *Module) ruleConflictsOn(ctx context.Context, tx *gorm.DB, entry *model.Entry) ([]guid.ID, error) {
	ids := []guid.ID{}
	if entry.Kind != "RULE" {
		return ids, nil
	}
	q := query.BindEntries(tx, m.names)
	for after := guid.ID(0); ; {
		read := q.WithContext(ctx).Where(q.TenantID.Eq(entry.TenantID), q.Kind.Eq("RULE"), q.State.Eq("ACTIVE"), q.ID.Gt(after), q.ID.Neq(entry.ID))
		if entry.AgentPrincipalID == nil {
			read = read.Where(q.Scope.Eq(entry.Scope))
			if entry.ProjectID != nil {
				read = read.Where(q.ProjectID.Eq(*entry.ProjectID))
			} else {
				read = read.Where(q.ProjectID.IsNull())
			}
		} else {
			read = read.Where(q.WithContext(ctx).Where(q.Scope.Eq("TEAM")).Or(q.AgentPrincipalID.Eq(*entry.AgentPrincipalID)))
		}
		rows, err := read.Order(q.ID).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			after = row.ID
			if conflictingRules(entry.Title+"\n"+entry.Body, row.Title+"\n"+row.Body) {
				ids = append(ids, row.ID)
				if len(ids) == 5 {
					return ids, nil
				}
			}
		}
		if len(rows) < 100 {
			return ids, nil
		}
	}
}

type AffectedExecutionPage struct {
	Items      []guid.ID `json:"items"`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

func (m *Module) affectedExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, entry guid.ID, cursor string, limit int) (AffectedExecutionPage, error) {
	out := AffectedExecutionPage{Items: []guid.ID{}}
	if limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	scope := scopeCursor(Scope{Scope: "AFFECTED:" + entry.String()}, tenant, authn.PrincipalIDFromContext(ctx), "INCLUSION")
	after, err := knowledgePosition(cursor, scope)
	if err != nil {
		return out, err
	}
	q := query.BindUsageReceipts(tx, m.names)
	err = q.WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "entry_id": entry, "kind": "INCLUSION"}).Where("execution_id > ?", after).Distinct("execution_id").Order("execution_id").Limit(limit+1).Pluck("execution_id", &out.Items).Error
	if err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
		out.NextCursor = knowledgeCursor(out.Items[len(out.Items)-1], scope)
	}
	return out, nil
}

type affectedExecutionsInput struct {
	KnowledgeTenantInput
	EntryID string `path:"entry_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor  string `query:"cursor" maxLength:"512"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

func (m *Module) registerReviewFactsHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "knowledge.affected_executions", Method: http.MethodGet, Path: entryPath + "/{entry_id}/affected-executions", Summary: "List verified historical execution inclusions for a managed memory", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, func(ctx context.Context, in *affectedExecutionsInput) (*respx.Body[AffectedExecutionPage], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		id, err := guid.RequireID(in.EntryID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		var page AffectedExecutionPage
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			view, err := m.entryOn(ctx, tx, tenant, id)
			if err != nil {
				return err
			}
			if _, err = m.authorize(ctx, tx, tenant, view.Scope, "knowledge_manage", true); err != nil {
				return err
			}
			page, err = m.affectedExecutionsOn(ctx, tx, tenant, id, in.Cursor, in.Limit)
			return err
		})
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		return respx.OK(ctx, page), nil
	})
}
