package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type SourcePathInput struct {
	KnowledgeTenantInput
	SourceID string `path:"source_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type sourceCreateInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SourceInput
}
type sourceSyncHTTPInput struct {
	SourcePathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SourceSyncInput
}
type sourceVersionHTTPInput struct {
	SourcePathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SourceVersionInput
}
type settingsHTTPInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           KnowledgeSettingsInput
}
type sourceListInput struct {
	Archived bool `query:"archived" default:"false"`
	KnowledgeTenantInput
	Scope            string `query:"scope" enum:"TEAM,PROJECT,AGENT" default:"TEAM"`
	ProjectID        string `query:"project_id"`
	AgentPrincipalID string `query:"agent_principal_id"`
	State            string `query:"state" enum:"PENDING,INDEXING,INDEXED,FAILED,INVALIDATED"`
	Cursor           string `query:"cursor" maxLength:"512"`
	Limit            int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type SourcePage struct {
	Items      []SourceView `json:"items"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor,omitempty"`
}
type sourceChunkListInput struct {
	SourcePathInput
	Cursor string `query:"cursor" maxLength:"512"`
	Offset int    `query:"offset" default:"0" minimum:"0" maximum:"2000" hidden:"true"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type SourceChunkPage struct {
	NextCursor string      `json:"next_cursor,omitempty"`
	Items      []ChunkView `json:"items"`
	HasMore    bool        `json:"has_more"`
	NextOffset int         `json:"next_offset"`
}
type lifecycleOutput[T any] struct {
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[T]
}

func (m *Module) registerSourceHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		operation := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 413, 422, 500, 503}}
		if id == "knowledge.sources.create" || id == "knowledge.sources.sync" || id == "knowledge.sources.ingest" {
			operation.MaxBodyBytes = 10 * 1024 * 1024
		}
		return operation
	}
	read := authz.Guard{Resource: "knowledge", Verb: "read"}
	write := authz.Guard{Resource: "knowledge", Verb: "write"}
	manage := authz.Guard{Resource: "knowledge", Verb: "manage"}
	authz.Register(m.registrar, api, op("knowledge.settings.get", http.MethodGet, "/knowledge/settings", "Read persisted knowledge settings and actual retrieval capabilities", 200), manage, m.settingsHTTP)
	authz.Register(m.registrar, api, op("knowledge.settings.update", http.MethodPut, "/knowledge/settings", "Update knowledge candidate policy with a current version", 200), manage, m.saveSettingsHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.list", http.MethodGet, "/knowledge/sources", "List scoped persistent knowledge source bindings", 200), read, m.sourcesHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.create", http.MethodPost, "/knowledge/sources", "Bind uploaded text or an explicitly unsupported remote source", 201), write, m.bindSourceHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.get", http.MethodGet, "/knowledge/sources/{source_id}", "Read a scoped knowledge source and its indexing facts", 200), read, m.sourceHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.chunks.list", http.MethodGet, "/knowledge/sources/{source_id}/chunks", "Read active source fragments with content hashes and retrieval entry IDs", 200), read, m.sourceChunksHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.usage.get", http.MethodGet, "/knowledge/sources/{source_id}/usage", "Read verified runtime inclusion and citation receipt counts or explicit unavailability", 200), read, m.sourceUsageHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.sync", http.MethodPost, "/knowledge/sources/{source_id}/syncs", "Synchronously index stored or replacement uploaded text", 201), write, m.syncSourceHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.ingest", http.MethodPost, "/knowledge/sources/{source_id}/ingestions", "Queue durable uploaded-text ingestion with live authorization on execution", 202), write, m.ingestSourceHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.reindex", http.MethodPost, "/knowledge/sources/{source_id}/reindexes", "Rebuild stored source fragments while retaining unchanged hashes", 201), write, m.reindexSourceHTTP)
	authz.Register(m.registrar, api, op("knowledge.sources.invalidate", http.MethodPost, "/knowledge/sources/{source_id}/invalidations", "Invalidate source fragments and revoke their searchable entries atomically", 201), write, m.invalidateSourceHTTP)
	if err := m.registerSourceLifecycleHTTP(api); err != nil {
		return err
	}
	if !m.registrar.IsDeclarationOnly() {
		for _, path := range []string{"/knowledge/settings", "/knowledge/sources", "/knowledge/sources/{source_id}/syncs", "/knowledge/sources/{source_id}/ingestions", "/knowledge/sources/{source_id}/reindexes", "/knowledge/sources/{source_id}/invalidations"} {
			if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
				return err
			}
		}
	}
	return nil
}

func lifecycleRead[T any](ctx context.Context, m *Module, rawTenant string, fn func(*gorm.DB, guid.ID) (T, error)) (*respx.Body[T], error) {
	tenant, err := guid.RequireID(rawTenant)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	var value T
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; value, e = fn(tx, tenant); return e })
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, value), nil
}

// Every replay rechecks current IAM through the source-owned scope; cached
// response content never serves as authorization evidence.
func lifecycleCommand[T any](ctx context.Context, m *Module, rawTenant, key, operation, method, path string, params map[string]string, body any, status int, fn func(*gorm.DB, guid.ID) (T, error), reauthorize func(*gorm.DB, guid.ID, T) error) (*lifecycleOutput[T], error) {
	tenant, err := guid.RequireID(rawTenant)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
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
	digest, err := idempotency.HashRequest(method, path, params, body)
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	k := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: operation, Value: key, RequestHash: digest}
	if err = k.Validate(); err != nil {
		return nil, knowledgeError(ctx, err)
	}
	var value T
	replayed := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, replayedResult, e := m.replay.Do(ctx, tx, k, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := fn(tx, tenant)
			if e != nil {
				return idempotency.Result{}, e
			}
			data, e := json.Marshal(v)
			return idempotency.Result{StatusCode: status, Body: data}, e
		})
		if e != nil {
			return e
		}
		replayed = replayedResult
		if e = json.Unmarshal(result.Body, &value); e != nil {
			return e
		}
		if replayed {
			return reauthorize(tx, tenant, value)
		}
		return nil
	})
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	out := &lifecycleOutput[T]{Body: respx.OK(ctx, value).Body}
	if replayed {
		out.Replayed = strconv.FormatBool(true)
	}
	return out, nil
}

func (m *Module) settingsHTTP(ctx context.Context, in *KnowledgeTenantInput) (*respx.Body[KnowledgeSettings], error) {
	return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (KnowledgeSettings, error) { return m.settingsOn(ctx, tx, tenant) })
}
func (m *Module) saveSettingsHTTP(ctx context.Context, in *settingsHTTPInput) (*lifecycleOutput[KnowledgeSettings], error) {
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.settings.update", http.MethodPut, "/knowledge/settings", map[string]string{"tenant_id": in.TenantID}, in.Body, 200, func(tx *gorm.DB, tenant guid.ID) (KnowledgeSettings, error) {
		return m.saveSettingsOn(ctx, tx, tenant, in.Body)
	}, func(tx *gorm.DB, tenant guid.ID, _ KnowledgeSettings) error {
		_, e := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_manage", true)
		return e
	})
}
func (m *Module) reauthorizeSource(ctx context.Context) func(*gorm.DB, guid.ID, SourceView) error {
	return func(tx *gorm.DB, tenant guid.ID, v SourceView) error {
		_, _, err := m.sourceOn(ctx, tx, tenant, v.ID, "knowledge_write", false)
		return err
	}
}
func (m *Module) bindSourceHTTP(ctx context.Context, in *sourceCreateInput) (*lifecycleOutput[SourceView], error) {
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.create", http.MethodPost, "/knowledge/sources", map[string]string{"tenant_id": in.TenantID}, in.Body, 201, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) { return m.bindSourceOn(ctx, tx, tenant, in.Body) }, m.reauthorizeSource(ctx))
}
func (m *Module) sourceHTTP(ctx context.Context, in *SourcePathInput) (*respx.Body[SourceView], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
		row, _, e := m.sourceOn(ctx, tx, tenant, id, "knowledge_read", false)
		if e != nil {
			return SourceView{}, e
		}
		return sourceView(row), nil
	})
}
func (m *Module) syncSourceHTTP(ctx context.Context, in *sourceSyncHTTPInput) (*lifecycleOutput[SourceView], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.sync", http.MethodPost, "/knowledge/sources/{source_id}/syncs", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 201, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
		return m.syncSourceOn(ctx, tx, tenant, id, in.Body, false)
	}, m.reauthorizeSource(ctx))
}
func (m *Module) ingestSourceHTTP(ctx context.Context, in *sourceSyncHTTPInput) (*lifecycleOutput[SourceView], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.ingest", http.MethodPost, "/knowledge/sources/{source_id}/ingestions", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 202, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
		return m.queueSourceOn(ctx, tx, tenant, id, in.Body)
	}, m.reauthorizeSource(ctx))
}
func (m *Module) reindexSourceHTTP(ctx context.Context, in *sourceVersionHTTPInput) (*lifecycleOutput[SourceView], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.reindex", http.MethodPost, "/knowledge/sources/{source_id}/reindexes", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 201, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
		return m.syncSourceOn(ctx, tx, tenant, id, SourceSyncInput{Version: in.Body.Version}, true)
	}, m.reauthorizeSource(ctx))
}
func (m *Module) invalidateSourceHTTP(ctx context.Context, in *sourceVersionHTTPInput) (*lifecycleOutput[SourceView], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.invalidate", http.MethodPost, "/knowledge/sources/{source_id}/invalidations", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 201, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
		return m.invalidateSourceOn(ctx, tx, tenant, id, in.Body.Version)
	}, m.reauthorizeSource(ctx))
}

func (m *Module) sourcesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in sourceListInput) (SourcePage, error) {
	out := SourcePage{Items: []SourceView{}}
	scope, err := listScope(knowledgeListInput{Scope: in.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID})
	if err != nil {
		return out, err
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, ErrInvalid
	}
	if in.State != "" && in.State != "PENDING" && in.State != "INDEXING" && in.State != "INDEXED" && in.State != "FAILED" && in.State != "INVALIDATED" {
		return out, ErrInvalid
	}
	if _, err = m.authorize(ctx, tx, tenant, scope, "knowledge_read", false); err != nil {
		return out, err
	}
	hash := scopeCursor(scope, tenant, authn.PrincipalIDFromContext(ctx), "sources:"+in.State+":"+strconv.FormatBool(in.Archived))
	after, err := knowledgePosition(in.Cursor, hash)
	if err != nil {
		return out, err
	}
	q := query.BindSources(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq(scope.Scope), q.ID.Gt(after))
	if in.Archived {
		read = read.Where(q.ArchivedAt.IsNotNull())
	} else {
		read = read.Where(q.ArchivedAt.IsNull())
	}
	if scope.ProjectID == nil {
		read = read.Where(q.ProjectID.IsNull())
	} else {
		read = read.Where(q.ProjectID.Eq(*scope.ProjectID))
	}
	if scope.AgentPrincipalID == nil {
		read = read.Where(q.AgentPrincipalID.IsNull())
	} else {
		read = read.Where(q.AgentPrincipalID.Eq(*scope.AgentPrincipalID))
	}
	if in.State != "" {
		read = read.Where(q.State.Eq(in.State))
	}
	rows, err := read.Order(q.ID).Limit(in.Limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > in.Limit
	if out.HasMore {
		rows = rows[:in.Limit]
	}
	for _, row := range rows {
		out.Items = append(out.Items, sourceView(row))
	}
	if out.HasMore {
		out.NextCursor = knowledgeCursor(rows[len(rows)-1].ID, hash)
	}
	return out, nil
}
func (m *Module) sourcesHTTP(ctx context.Context, in *sourceListInput) (*respx.Body[SourcePage], error) {
	return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (SourcePage, error) { return m.sourcesOn(ctx, tx, tenant, *in) })
}
func (m *Module) sourceChunksOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, offset, limit int) (SourceChunkPage, error) {
	out := SourceChunkPage{Items: []ChunkView{}}
	if offset < 0 || offset > 2000 || limit < 1 || limit > 200 {
		return out, ErrInvalid
	}
	if _, _, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_read", false); err != nil {
		return out, err
	}
	q := query.BindChunks(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SourceID.Eq(id), q.InvalidatedAt.IsNull()).Order(q.Ordinal, q.ID).Offset(offset).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, r := range rows {
		out.Items = append(out.Items, ChunkView{ID: r.ID, SourceID: r.SourceID, EntryID: r.EntryID, Ordinal: r.Ordinal, Kind: r.Kind, HeadingPath: r.HeadingPath, Content: r.Content, ContentHash: r.ContentHash, CharCount: r.CharCount, TokenEstimate: r.TokenEstimate})
	}
	out.NextOffset = offset + len(rows)
	return out, nil
}
func (m *Module) sourceChunksHTTP(ctx context.Context, in *sourceChunkListInput) (*respx.Body[SourceChunkPage], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (SourceChunkPage, error) {
		return m.sourceChunksCursorOn(ctx, tx, tenant, id, in.Cursor, in.Offset, in.Limit)
	})
}
func (m *Module) sourceUsageHTTP(ctx context.Context, in *SourcePathInput) (*respx.Body[SourceUsage], error) {
	id, err := guid.RequireID(in.SourceID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, tenant guid.ID) (SourceUsage, error) { return m.sourceUsageOn(ctx, tx, tenant, id) })
}
