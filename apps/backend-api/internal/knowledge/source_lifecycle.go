package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

var errSourceUploadTooLarge = errors.New("knowledge source upload too large")
var errSourceUploadFormat = errors.New("knowledge source upload format unsupported")

type SourceMetadataInput struct {
	Version int      `json:"version" minimum:"1"`
	Title   string   `json:"title" minLength:"1" maxLength:"200"`
	Tags    []string `json:"tags" maxItems:"10"`
}
type sourceMetadataHTTPInput struct {
	SourcePathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SourceMetadataInput
}
type sourceUploadForm struct {
	Files []huma.FormFile `form:"files" contentType:"application/octet-stream" required:"true"`
}
type sourceUploadInput struct {
	KnowledgeTenantInput
	IdempotencyKey   string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Scope            string `query:"scope" enum:"TEAM,PROJECT,AGENT" default:"TEAM"`
	ProjectID        string `query:"project_id"`
	AgentPrincipalID string `query:"agent_principal_id"`
	RawBody          huma.MultipartFormFiles[sourceUploadForm]
}

func (m *Module) sourceMetadataOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SourceMetadataInput) (SourceView, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Version < 1 || in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || len(in.Tags) > 10 {
		return SourceView{}, ErrInvalid
	}
	tags := []string{}
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || utf8.RuneCountInString(tag) > 32 {
			return SourceView{}, ErrInvalid
		}
		if !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	row, kind, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if row.Version != in.Version || row.ArchivedAt != nil || row.State == "INDEXING" {
		return SourceView{}, ErrConflict
	}
	if row.SourceKind != "UPLOAD" && in.Title != row.Title {
		return SourceView{}, ErrInvalid
	}
	raw, _ := json.Marshal(tags)
	q := query.BindSources(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"title": in.Title, "tags_json": string(raw), "version": in.Version + 1, "updated_at": m.now(), "updated_by": authn.PrincipalIDFromContext(ctx)})
	if err != nil {
		return SourceView{}, err
	}
	if result.RowsAffected != 1 {
		return SourceView{}, ErrConflict
	}
	// Refresh canonical search titles and hashes; frozen inclusion evidence stays unchanged.
	c := query.BindChunks(tx, m.names)
	e := query.BindEntries(tx, m.names)
	entries := c.WithContext(ctx).Select(c.EntryID).Where(c.TenantID.Eq(tenant), c.SourceID.Eq(id), c.InvalidatedAt.IsNull())
	entryRows, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.WithContext(ctx).Columns(e.ID).In(entries)).Find()
	if err != nil {
		return SourceView{}, err
	}
	for _, entry := range entryRows {
		if _, err = e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ID.Eq(entry.ID)).Updates(map[string]any{"title": in.Title, "content_hash": sourceHash(in.Title + "\n" + entry.Body), "version": entry.Version + 1, "updated_at": m.now()}); err != nil {
			return SourceView{}, err
		}
	}
	row.Title, row.TagsJSON, row.Version, row.UpdatedAt = in.Title, string(raw), in.Version+1, m.now()
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(sourceScope(row)), id, "knowledge_source", row.Version, kind, "knowledge_write", "knowledge.source_metadata_changed", map[string]any{"tags": tags}); err != nil {
		return SourceView{}, err
	}
	return sourceView(row), nil
}

func (m *Module) archiveSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int) (SourceView, error) {
	row, kind, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if version < 1 {
		return SourceView{}, ErrInvalid
	}
	if row.Version != version || row.ArchivedAt != nil {
		return SourceView{}, ErrConflict
	}
	if row.SourceKind != "UPLOAD" {
		return SourceView{}, ErrInvalid
	}
	if err = m.invalidateSourceChunksOn(ctx, tx, tenant, row, nil); err != nil {
		return SourceView{}, err
	}
	now := m.now()
	q := query.BindSources(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"archived_at": now, "state": "INVALIDATED", "state_reason": "ARCHIVED", "chunk_count": 0, "version": version + 1, "updated_at": now, "updated_by": authn.PrincipalIDFromContext(ctx)})
	if err != nil {
		return SourceView{}, err
	}
	if result.RowsAffected != 1 {
		return SourceView{}, ErrConflict
	}
	row.ArchivedAt, row.State, row.StateReason, row.ChunkCount, row.Version, row.UpdatedAt = &now, "INVALIDATED", "ARCHIVED", 0, version+1, now
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(sourceScope(row)), id, "knowledge_source", row.Version, kind, "knowledge_write", "knowledge.source_archived", map[string]any{"state": row.State}); err != nil {
		return SourceView{}, err
	}
	return sourceView(row), nil
}

func (m *Module) sourceChunksCursorOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, cursor string, offset, limit int) (SourceChunkPage, error) {
	out := SourceChunkPage{Items: []ChunkView{}}
	if limit < 1 || limit > 200 || offset < 0 || offset > 2000 || cursor != "" && offset != 0 {
		return out, ErrInvalid
	}
	row, _, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_read", false)
	if err != nil {
		return out, err
	}
	scope := scopeCursor(sourceScope(row), tenant, authn.PrincipalIDFromContext(ctx), fmt.Sprintf("chunks:%s:%d", id, row.Version))
	after, err := knowledgePosition(cursor, scope)
	if err != nil {
		return out, err
	}
	q := query.BindChunks(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SourceID.Eq(id), q.InvalidatedAt.IsNull(), q.Ordinal.Gte(int(after)))
	rows, err := read.Order(q.Ordinal, q.ID).Offset(offset).Limit(limit + 1).Find()
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
	if out.HasMore {
		out.NextCursor = knowledgeCursor(guid.ID(rows[len(rows)-1].Ordinal+1), scope)
	}
	return out, nil
}

func uploadSourceInputs(files []huma.FormFile, scope Scope) ([]SourceInput, error) {
	if !validScope(scope) || len(files) < 1 || len(files) > 10 {
		return nil, ErrInvalid
	}
	out := []SourceInput{}
	for _, file := range files {
		if !file.IsSet {
			return nil, ErrInvalid
		}
		raw, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > 10<<20 {
			return nil, errSourceUploadTooLarge
		}
		name := filepath.Base(strings.ReplaceAll(file.Filename, "\\", "/"))
		format := "CODE"
		switch strings.ToLower(filepath.Ext(name)) {
		case ".pdf":
			return nil, ErrUnavailable
		case ".md", ".markdown":
			format = "MARKDOWN"
		case ".txt":
			format = "TEXT"
		case ".go", ".js", ".ts", ".tsx", ".py", ".rs", ".java", ".css", ".html", ".json", ".yaml", ".yml", ".sql", ".sh":
		default:
			return nil, errSourceUploadFormat
		}
		if !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 || strings.HasPrefix(string(raw), "%PDF-") {
			return nil, ErrInvalid
		}
		out = append(out, SourceInput{Scope: scope, SourceKind: "UPLOAD", Title: name, Locator: name, Format: format, Content: string(raw)})
	}
	return out, nil
}

func (m *Module) registerSourceLifecycleHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 413, 415, 422, 500, 503}}
	}
	write := authz.Guard{Resource: "knowledge", Verb: "write"}
	upload := op("knowledge.sources.upload", http.MethodPost, "/knowledge/source-uploads", "Upload at most ten UTF-8 documents, each at most 10 MiB", 201)
	upload.MaxBodyBytes = 101 << 20
	authz.Register(m.registrar, api, upload, write, func(ctx context.Context, in *sourceUploadInput) (*lifecycleOutput[[]SourceView], error) {
		scope, err := listScope(knowledgeListInput{Scope: in.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID})
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		inputs, err := uploadSourceInputs(in.RawBody.Data().Files, scope)
		if err != nil {
			return nil, knowledgeError(ctx, err)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.upload", http.MethodPost, "/knowledge/source-uploads", map[string]string{"tenant_id": in.TenantID}, inputs, 201, func(tx *gorm.DB, tenant guid.ID) ([]SourceView, error) {
			out := []SourceView{}
			for _, input := range inputs {
				row, e := m.bindSourceOn(ctx, tx, tenant, input)
				if e != nil {
					return nil, e
				}
				out = append(out, row)
			}
			return out, nil
		}, func(tx *gorm.DB, tenant guid.ID, rows []SourceView) error {
			for _, row := range rows {
				if _, _, e := m.sourceOn(ctx, tx, tenant, row.ID, "knowledge_write", false); e != nil {
					return e
				}
			}
			return nil
		})
	})
	authz.Register(m.registrar, api, op("knowledge.sources.update", http.MethodPatch, "/knowledge/sources/{source_id}", "Edit source title and tags with a current version", 200), write, func(ctx context.Context, in *sourceMetadataHTTPInput) (*lifecycleOutput[SourceView], error) {
		id, err := guid.RequireID(in.SourceID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.update", http.MethodPatch, "/knowledge/sources/{source_id}", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 200, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
			return m.sourceMetadataOn(ctx, tx, tenant, id, in.Body)
		}, m.reauthorizeSource(ctx))
	})
	authz.Register(m.registrar, api, op("knowledge.sources.archive", http.MethodPost, "/knowledge/sources/{source_id}/archives", "Archive a source and invalidate future retrieval atomically", 201), write, func(ctx context.Context, in *sourceVersionHTTPInput) (*lifecycleOutput[SourceView], error) {
		id, err := guid.RequireID(in.SourceID)
		if err != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.sources.archive", http.MethodPost, "/knowledge/sources/{source_id}/archives", map[string]string{"tenant_id": in.TenantID, "source_id": in.SourceID}, in.Body, 201, func(tx *gorm.DB, tenant guid.ID) (SourceView, error) {
			return m.archiveSourceOn(ctx, tx, tenant, id, in.Body.Version)
		}, m.reauthorizeSource(ctx))
	})
	if !m.registrar.IsDeclarationOnly() {
		for _, path := range []string{"/knowledge/source-uploads", "/knowledge/sources/{source_id}", "/knowledge/sources/{source_id}/archives"} {
			if err := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); err != nil {
				return err
			}
		}
	}
	return nil
}
