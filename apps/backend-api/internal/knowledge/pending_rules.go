package knowledge

import (
	"context"
	"errors"
	"net/http"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type PendingRule struct {
	ID, ProjectID    guid.ID
	Scope            string
	AgentPrincipalID guid.ID
	Title            string
	CreatedAt        time.Time
}

// PendingRules reads current independent Human review candidates with both
// source read permission and the existing approval authority check.
type PendingRules func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]PendingRule, error)

func (m *Module) providePendingRules(s *module.Services) error {
	if err := module.Provide[NotificationLink](s, m.notificationLinkOn); err != nil {
		return err
	}
	if err := module.Provide[NotificationAudience](s, m.notificationAudienceOn); err != nil {
		return err
	}
	return module.Provide[PendingRules](s, m.pendingRulesOn)
}

type NotificationLink func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error)

func (m *Module) notificationLinkOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, typ string) (string, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || (typ != "memory.reviewed" && typ != "knowledge.source_failed") {
		return "", ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", ErrInvalid
	}
	root := "/admin/" + tenant.String()
	if typ == "knowledge.source_failed" {
		return root + "/knowledge/sources/" + id.String(), nil
	}
	q := query.BindEntries(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return "", err
	}
	if r.ProjectID != nil {
		root += "/entities/" + r.ProjectID.String()
	}
	path := root + "/governance/memory-review?entry_id=" + id.String()
	if r.Scope == "AGENT" && r.AgentPrincipalID != nil {
		path += "&scope=AGENT&agent_principal_id=" + r.AgentPrincipalID.String()
	}
	return path, nil
}

// NotificationAudience returns source scope and eligibility, never document text.
type NotificationAudience func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error)

func (m *Module) notificationAudienceOn(ctx context.Context, tx *gorm.DB, tenant, id, recipient guid.ID, typ string) (guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 || recipient <= 0 {
		return 0, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalid
	}
	var scope Scope
	var owner guid.ID
	switch typ {
	case "memory.reviewed":
		q := query.BindEntries(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Kind.Eq("RULE"), q.State.In("ACTIVE", "REJECTED")).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		// An actual review receipt distinguishes approval from a rule born ACTIVE.
		r := query.BindReviews(tx, m.names)
		n, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.EntryID.Eq(id), r.Action.In("APPROVED", "REJECTED")).Count()
		if err != nil || n == 0 {
			return 0, false, err
		}
		scope = Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}
		owner = row.CreatedBy
	case "knowledge.source_failed":
		q := query.BindSources(tx, m.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.State.Eq("FAILED")).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, err
		}
		scope = Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}
		owner = row.CreatedBy
	default:
		return 0, false, ErrInvalid
	}
	project := guid.ID(0)
	if scope.ProjectID != nil {
		project = *scope.ProjectID
	}
	if owner != recipient {
		if typ != "memory.reviewed" || m.agents.ByPrincipalOn == nil {
			return project, false, nil
		}
		agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, owner)
		if err != nil || !found || agent.OwnerPrincipalID != recipient {
			return project, false, err
		}
	}
	readCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: recipient})
	_, err := m.authorize(readCtx, tx, tenant, scope, "knowledge_read", true)
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return project, false, nil
	}
	return project, err == nil, err
}

func (m *Module) pendingRulesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]PendingRule, error) {
	if m == nil {
		return nil, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrForbidden
	}
	q := query.BindEntries(tx, m.names)
	stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Kind.Eq("RULE"), q.State.Eq("PENDING_REVIEW"))
	if project > 0 {
		stmt = stmt.Where(q.ProjectID.Eq(project))
	}
	rows, err := stmt.Find()
	if err != nil {
		return nil, err
	}
	out := []PendingRule{}
	for _, row := range rows {
		_, err := m.authorize(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, "knowledge_read", true)
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		allowed, err := m.ruleNotificationEligible(ctx, tx, tenant, row.ID, actor)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		item := PendingRule{ID: row.ID, Title: row.Title, CreatedAt: row.CreatedAt, Scope: row.Scope}
		if row.AgentPrincipalID != nil {
			item.AgentPrincipalID = *row.AgentPrincipalID
		}
		if row.ProjectID != nil {
			item.ProjectID = *row.ProjectID
		}
		out = append(out, item)
	}
	return out, nil
}

type entryInput struct {
	KnowledgeTenantInput
	EntryID string `path:"entry_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerEntryHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.get", Method: http.MethodGet, Path: entryPath + "/{entry_id}", Summary: "Read an exact knowledge entry under current source scope", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, m.entryHTTP)
}

func (m *Module) entryOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (EntryView, error) {
	if tenant <= 0 || id <= 0 || tx == nil {
		return EntryView{}, ErrInvalid
	}
	q := query.BindEntries(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return EntryView{}, ErrNotFound
	}
	if err != nil {
		return EntryView{}, err
	}
	scope := Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}
	if _, err := m.authorize(ctx, tx, tenant, scope, "knowledge_read", row.State != "ACTIVE"); err != nil {
		return EntryView{}, err
	}
	if row.State == "PENDING_REVIEW" {
		if _, err := m.authorize(ctx, tx, tenant, scope, "memory_approve", true); err != nil {
			return EntryView{}, err
		}
	}
	view := entryView(row)
	if row.State == "PENDING_REVIEW" {
		view.ConflictMemoryIDs, err = m.ruleConflictsOn(ctx, tx, row)
	}
	return view, err
}

func (m *Module) entryHTTP(ctx context.Context, in *entryInput) (*respx.Body[EntryView], error) {
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
	var out EntryView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var err error; out, err = m.entryOn(ctx, tx, tenant, id); return err })
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
