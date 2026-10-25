package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	knowledgei18n "7link.com/platform/internal/knowledge/i18n"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const entryPath = "/knowledge"

type KnowledgeTenantInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type knowledgeCreateInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           EntryInput
}
type knowledgeListInput struct {
	KnowledgeTenantInput
	Query            string `query:"search" maxLength:"500"`
	Scope            string `query:"scope" enum:"TEAM,PROJECT,AGENT" default:"TEAM"`
	ProjectID        string `query:"project_id"`
	AgentPrincipalID string `query:"agent_principal_id"`
	State            string `query:"state" enum:"ACTIVE,PENDING_REVIEW,REJECTED,REVOKED" default:"ACTIVE"`
	Cursor           string `query:"cursor" maxLength:"512"`
	Limit            int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type knowledgeReviewInput struct {
	KnowledgeTenantInput
	EntryID        string `path:"entry_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"64"`
	Body           ReviewInput
}
type KnowledgePage struct {
	Items      []EntryView `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
}
type knowledgeCommandOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[EntryView]
}

func (m *Module) database() (*gorm.DB, error) {
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}
func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m.registrar == nil {
		return ErrUnavailable
	}
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 429, 500, 503}}
	}
	// Membership is only the transport guard. Each command evaluates the exact
	// TEAM/PROJECT/AGENT scope with the transactional IAM capability.
	m.registerRetrievalDebugHTTP(api)
	m.registerNativeSearchHTTP(api)
	m.registerEmbeddingHTTP(api)
	m.registerDiagnosticsHTTP(api)
	m.registerInjectionHTTP(api)
	if err := m.registerDistillationHTTP(api); err != nil {
		return err
	}
	if err := m.registerSkillsHTTP(api); err != nil {
		return err
	}
	if err := m.registerMemoryHTTP(api); err != nil {
		return err
	}
	m.registerReviewFactsHTTP(api)
	if err := m.registerSourceHTTP(api); err != nil {
		return err
	}
	m.registerEntryHTTP(api)
	authz.Register(m.registrar, api, op("knowledge.list", http.MethodGet, entryPath, "List scoped knowledge or a memory review queue", 200), authz.Guard{Resource: "knowledge", Verb: "read"}, m.listHTTP)
	createOperation := op("knowledge.create", http.MethodPost, entryPath, "Write a knowledge entry or propose a rule memory", 201)
	// Spec06 BR-06-35 returns the existing memory on content deduplication.
	authz.Register(m.registrar, api, createOperation, authz.Guard{Resource: "knowledge", Verb: "write"}, m.createHTTP)
	if path := api.OpenAPI().Paths["/tenants/{tenant_id}"+entryPath]; path != nil && path.Post != nil {
		// Both success statuses carry the same entry envelope and replay header.
		path.Post.Responses["200"] = path.Post.Responses["201"]
	}
	authz.Register(m.registrar, api, op("knowledge.review", http.MethodPost, entryPath+"/{entry_id}/reviews", "Review or revoke a knowledge memory", 201), authz.Guard{Resource: "memory", Verb: "approve"}, m.reviewHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+entryPath); err != nil {
		return err
	}
	return authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+entryPath+"/{entry_id}/reviews")
}
func knowledgeError(ctx context.Context, err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if mapped, ok := skillError(ctx, err); ok {
		return mapped
	}
	switch {
	case errors.Is(err, ErrMemoryQuota):
		now := time.Now().UTC()
		retry := int(now.Truncate(24*time.Hour).Add(24*time.Hour).Sub(now).Seconds()) + 1
		return huma.ErrorWithHeaders(respx.ErrStatus(ctx, 429, 150429, knowledgei18n.MemoryQuota, map[string]string{"reason_code": "MEMORY_QUOTA"}), http.Header{"Retry-After": {strconv.Itoa(retry)}})
	case errors.Is(err, ErrMemorySecret):
		return respx.ErrStatus(ctx, 422, 150425, knowledgei18n.MemorySecret, map[string]string{"reason_code": "MEMORY_SECRET"})
	case errors.Is(err, ErrMemoryProvenance):
		return respx.ErrStatus(ctx, 422, 150424, knowledgei18n.MemoryProvenance, map[string]string{"reason_code": "MEMORY_PROVENANCE"})
	case errors.Is(err, ErrRemoteAccess):
		return respx.ErrStatus(ctx, 503, 160503, knowledgei18n.Unavailable, map[string]string{"reason_code": "REMOTE_ACCESS_DENIED"})
	case errors.Is(err, ErrRemoteMissing):
		return respx.ErrStatus(ctx, 503, 160503, knowledgei18n.Unavailable, map[string]string{"reason_code": "REMOTE_DOCUMENT_NOT_FOUND"})
	case errors.Is(err, errSourceUploadTooLarge):
		return respx.ErrStatus(ctx, 413, 160413, knowledgei18n.Invalid, nil)
	case errors.Is(err, errSourceUploadFormat):
		return respx.ErrStatus(ctx, 415, 160415, knowledgei18n.Invalid, nil)
	case errors.Is(err, ErrConflictUnacknowledged):
		return respx.ErrStatus(ctx, 422, 160422, knowledgei18n.Invalid, map[string]string{"reason_code": "CONFLICT_UNACKNOWLEDGED"})
	case errors.Is(err, ErrSelfReview):
		return respx.ErrStatus(ctx, 403, 160403, knowledgei18n.Forbidden, map[string]string{"reason_code": "SELF_REVIEW"})
	case errors.Is(err, ErrForbidden):
		return respx.ErrStatus(ctx, 403, 160403, knowledgei18n.Forbidden, nil)
	case errors.Is(err, ErrNotFound):
		return respx.ErrStatus(ctx, 404, 160404, knowledgei18n.NotFound, nil)
	case errors.Is(err, ErrConflict), errors.Is(err, idempotency.ErrKeyReused), errors.Is(err, idempotency.ErrInProgress):
		return respx.ErrStatus(ctx, 409, 160409, knowledgei18n.Conflict, nil)
	case errors.Is(err, ErrInvalid), errors.Is(err, idempotency.ErrInvalidKey):
		return respx.ErrStatus(ctx, 422, 160422, knowledgei18n.Invalid, nil)
	case errors.Is(err, ErrUnavailable):
		return respx.ErrStatus(ctx, 503, 160503, knowledgei18n.Unavailable, nil)
	default:
		return respx.ErrStatus(ctx, 500, 160500, respx.InternalServerError, nil)
	}
}
func listScope(in knowledgeListInput) (Scope, error) {
	s := Scope{Scope: in.Scope}
	if in.ProjectID != "" {
		id, e := guid.RequireID(in.ProjectID)
		if e != nil {
			return s, ErrInvalid
		}
		s.ProjectID = &id
	}
	if in.AgentPrincipalID != "" {
		id, e := guid.RequireID(in.AgentPrincipalID)
		if e != nil {
			return s, ErrInvalid
		}
		s.AgentPrincipalID = &id
	}
	if !validScope(s) {
		return s, ErrInvalid
	}
	return s, nil
}
func scopeCursor(s Scope, tenant, actor guid.ID, state string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(tenant.String()+":"+actor.String()+":"+state+":"+string(b))))
}
func knowledgeCursor(pos guid.ID, scope string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(pos.String() + ":" + scope))
}
func knowledgePosition(raw, scope string) (guid.ID, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > 512 {
		return 0, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return 0, ErrInvalid
	}
	pos, hash, found := strings.Cut(string(b), ":")
	if !found || hash != scope {
		return 0, ErrInvalid
	}
	id, e := guid.RequireID(pos)
	if e != nil {
		return 0, ErrInvalid
	}
	return id, nil
}
func (m *Module) listOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in knowledgeListInput) (KnowledgePage, error) {
	out := KnowledgePage{Items: []EntryView{}}
	scope, err := listScope(in)
	if err != nil {
		return out, err
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, ErrInvalid
	}
	permission := "knowledge_read"
	if in.State == "PENDING_REVIEW" {
		permission = "memory_approve"
		ctx = memoryApprovalContext(ctx)
	}
	if in.State != "ACTIVE" && in.State != "PENDING_REVIEW" && in.State != "REJECTED" && in.State != "REVOKED" {
		return out, ErrInvalid
	}
	if _, err = m.authorize(ctx, tx, tenant, scope, permission, in.State != "ACTIVE"); err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Query) != "" {
		return m.listSearchOn(ctx, tx, tenant, in)
	}
	hash := scopeCursor(scope, tenant, authn.PrincipalIDFromContext(ctx), in.State)
	after, err := knowledgePosition(in.Cursor, hash)
	if err != nil {
		return out, err
	}
	q := query.BindEntries(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq(scope.Scope), q.State.Eq(in.State), q.ID.Gt(after))
	if scope.ProjectID == nil {
		request = request.Where(q.ProjectID.IsNull())
	} else {
		request = request.Where(q.ProjectID.Eq(*scope.ProjectID))
	}
	if scope.AgentPrincipalID == nil {
		request = request.Where(q.AgentPrincipalID.IsNull())
	} else {
		request = request.Where(q.AgentPrincipalID.Eq(*scope.AgentPrincipalID))
	}
	if in.State == "PENDING_REVIEW" {
		request = request.Where(q.Kind.Eq("RULE"))
	}
	rows, err := request.Order(q.ID).Limit(in.Limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > in.Limit
	if out.HasMore {
		rows = rows[:in.Limit]
	}
	for _, row := range rows {
		view := entryView(row)
		if row.State == "PENDING_REVIEW" {
			view.ConflictMemoryIDs, err = m.ruleConflictsOn(ctx, tx, row)
			if err != nil {
				return out, err
			}
		}
		out.Items = append(out.Items, view)
	}
	if out.HasMore {
		out.NextCursor = knowledgeCursor(rows[len(rows)-1].ID, hash)
	}
	return out, nil
}
func (m *Module) listHTTP(ctx context.Context, in *knowledgeListInput) (*respx.Body[KnowledgePage], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	var out KnowledgePage
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.listOn(ctx, tx, tenant, *in); return e })
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) command(ctx context.Context, tenant guid.ID, key, operation, path string, params map[string]string, body any, fn func(*gorm.DB) (EntryView, error)) (*knowledgeCommandOutput, error) {
	if authn.TenantIDFromContext(ctx) != tenant {
		return nil, knowledgeError(ctx, ErrForbidden)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	if m.replay == nil {
		return nil, knowledgeError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(http.MethodPost, path, params, body)
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: hash}
	if key != "" {
		if err = k.Validate(); err != nil {
			return nil, knowledgeError(ctx, err)
		}
	}
	var v EntryView
	var replayed bool
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if key == "" {
			var e error
			v, e = fn(tx)
			return e
		}
		result, replayedResult, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			entry, e := fn(tx)
			if e != nil {
				return idempotency.Result{}, e
			}
			b, e := json.Marshal(entry)
			status := http.StatusCreated
			if entry.Deduplicated {
				status = http.StatusOK
			}
			return idempotency.Result{StatusCode: status, Body: b, ResourceKind: "knowledge_entry", ResourceID: entry.ID}, e
		})
		if e != nil {
			return e
		}
		replayed = replayedResult
		if e = json.Unmarshal(result.Body, &v); e != nil {
			return e
		}
		if replayed {
			permission, humanOnly := "knowledge_read", false
			if operation == "knowledge.memory.edit" {
				permission = "knowledge_write"
			}
			if operation == "knowledge.memory.promote" {
				permission, humanOnly = "memory_approve", true
			}
			if review, ok := body.(ReviewInput); operation == "knowledge.review" && ok {
				permission, humanOnly = "memory_approve", true
				if review.Action == "REVOKED" {
					permission = "knowledge_manage"
				} else if v.CreatedBy == authn.PrincipalIDFromContext(ctx) {
					return ErrSelfReview
				}
			}
			if permission == "memory_approve" {
				ctx = memoryApprovalContext(ctx)
			}
			_, e = m.authorize(ctx, tx, tenant, v.Scope, permission, humanOnly)
			return e
		}
		return nil
	})
	if err != nil {
		if deniedErr := m.recordMemoryDenied(ctx, tenant, body, err); deniedErr != nil {
			return nil, knowledgeError(ctx, deniedErr)
		}
		return nil, knowledgeError(ctx, err)
	}
	status := http.StatusCreated
	if v.Deduplicated {
		status = http.StatusOK
	}
	out := &knowledgeCommandOutput{Status: status, Body: respx.OK(ctx, v).Body}
	if replayed {
		out.Replayed = strconv.FormatBool(true)
	}
	return out, nil
}
func (m *Module) createHTTP(ctx context.Context, in *knowledgeCreateInput) (*knowledgeCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return m.command(ctx, tenant, in.IdempotencyKey, "knowledge.create", entryPath, map[string]string{"tenant_id": in.TenantID}, in.Body, func(tx *gorm.DB) (EntryView, error) { return m.writeOn(ctx, tx, tenant, in.Body) })
}
func (m *Module) reviewHTTP(ctx context.Context, in *knowledgeReviewInput) (*knowledgeCommandOutput, error) {
	tenant, e := guid.RequireID(in.TenantID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	id, e := guid.RequireID(in.EntryID)
	if e != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return m.command(ctx, tenant, in.IdempotencyKey, "knowledge.review", entryPath+"/{entry_id}/reviews", map[string]string{"tenant_id": in.TenantID, "entry_id": in.EntryID}, in.Body, func(tx *gorm.DB) (EntryView, error) { return m.reviewOn(ctx, tx, tenant, id, in.Body) })
}

var _ module.HTTPRegistrar = (*Module)(nil)
