package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type KnowledgeSettings struct {
	IndexStrategy           string     `json:"index_strategy" enum:"LEXICAL,FULL_TEXT,HYBRID"`
	SourcePermission        string     `json:"source_permission" enum:"WRITERS,MANAGERS"`
	MemoryApproval          string     `json:"memory_approval" enum:"RULES_ONLY,ALL_MEMORIES"`
	EmbeddingRequirements   []string   `json:"embedding_requirements"`
	CandidateLimit          int        `json:"candidate_limit" minimum:"1" maximum:"200"`
	Version                 int        `json:"version" minimum:"0"`
	Engine                  string     `json:"engine" enum:"LEXICAL_ENTRIES,NATIVE_FULL_TEXT,K2"`
	Dialect                 string     `json:"dialect"`
	FullTextAvailable       bool       `json:"full_text_available"`
	HybridAvailable         bool       `json:"hybrid_available"`
	RemoteSyncAvailable     bool       `json:"remote_sync_available"`
	URLSyncAvailable        bool       `json:"url_sync_available"`
	RepositorySyncAvailable bool       `json:"repository_sync_available"`
	CapabilityReason        string     `json:"capability_reason"`
	UpdatedAt               *time.Time `json:"updated_at,omitempty"`
}
type KnowledgeSettingsInput struct {
	IndexStrategy    string `json:"index_strategy,omitempty" enum:"LEXICAL,FULL_TEXT,HYBRID"`
	SourcePermission string `json:"source_permission,omitempty" enum:"WRITERS,MANAGERS"`
	MemoryApproval   string `json:"memory_approval,omitempty" enum:"RULES_ONLY,ALL_MEMORIES"`
	CandidateLimit   int    `json:"candidate_limit" minimum:"1" maximum:"200"`
	Version          int    `json:"version" minimum:"0" doc:"Zero creates persisted settings; subsequent writes require the current version."`
}

func (m *Module) settingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (KnowledgeSettings, error) {
	if _, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_manage", true); err != nil {
		return KnowledgeSettings{}, err
	}
	return m.settingsValueOn(ctx, tx, tenant)
}
func (m *Module) settingsValueOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (KnowledgeSettings, error) {
	out := KnowledgeSettings{CandidateLimit: 50, Engine: "LEXICAL_ENTRIES", Dialect: tx.Dialector.Name(), CapabilityReason: "FULL_TEXT_AND_VECTOR_INDEX_NOT_INSTALLED;REMOTE_CONNECTOR_NOT_CONFIGURED"}
	out.URLSyncAvailable = m.remote.supports("URL")
	out.RepositorySyncAvailable = m.remote.supports("REPOSITORY")
	out.RemoteSyncAvailable = out.URLSyncAvailable || out.RepositorySyncAvailable
	if out.RemoteSyncAvailable {
		out.CapabilityReason = "FULL_TEXT_AND_VECTOR_INDEX_NOT_INSTALLED"
		if !out.RepositorySyncAvailable {
			out.CapabilityReason += ";REPOSITORY_CONNECTOR_NOT_CONFIGURED"
		}
	}
	out.FullTextAvailable = query.EnsureNativeIndex(tx.WithContext(ctx), m.names, m.native) == nil
	out.HybridAvailable = m.config.Embedding.Enabled && tx.Dialector.Name() == "postgres" && query.ProbeVector(tx, m.native) == nil
	if out.FullTextAvailable {
		out.Engine = "NATIVE_FULL_TEXT"
		out.CapabilityReason = ""
	} else {
		out.CapabilityReason = "NATIVE_FTS_UNAVAILABLE"
	}
	if !out.HybridAvailable {
		out.CapabilityReason += ";PGVECTOR_OR_EMBEDDING_UNAVAILABLE"
	}
	out.IndexStrategy, out.SourcePermission, out.MemoryApproval = "LEXICAL", "WRITERS", "RULES_ONLY"
	out.EmbeddingRequirements = []string{"POSTGRESQL_VECTOR_MODULE", "COMPATIBLE_EMBEDDING_MODEL", "SUPPORTED_DIMENSIONS", "SEALED_API_CREDENTIAL"}
	q := query.BindSettings(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.CandidateLimit = row.CandidateLimit
	out.IndexStrategy, out.SourcePermission, out.MemoryApproval = row.IndexStrategy, row.SourcePermission, row.MemoryApproval
	out.Version = row.Version
	out.UpdatedAt = &row.UpdatedAt
	return out, nil
}
func (m *Module) saveSettingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in KnowledgeSettingsInput) (KnowledgeSettings, error) {
	current, err := m.settingsOn(ctx, tx, tenant)
	if err != nil {
		return KnowledgeSettings{}, err
	}
	if in.IndexStrategy == "" {
		in.IndexStrategy = current.IndexStrategy
	}
	if in.SourcePermission == "" {
		in.SourcePermission = current.SourcePermission
	}
	if in.MemoryApproval == "" {
		in.MemoryApproval = current.MemoryApproval
	}
	if !slices.Contains([]string{"LEXICAL", "FULL_TEXT", "HYBRID"}, in.IndexStrategy) {
		return KnowledgeSettings{}, ErrInvalid
	}
	if in.IndexStrategy == "FULL_TEXT" && !current.FullTextAvailable || in.IndexStrategy == "HYBRID" && !current.HybridAvailable {
		return KnowledgeSettings{}, ErrUnavailable
	}
	if !slices.Contains([]string{"WRITERS", "MANAGERS"}, in.SourcePermission) || !slices.Contains([]string{"RULES_ONLY", "ALL_MEMORIES"}, in.MemoryApproval) {
		return KnowledgeSettings{}, ErrInvalid
	}
	if in.CandidateLimit < 1 || in.CandidateLimit > 200 || in.Version < 0 {
		return KnowledgeSettings{}, ErrInvalid
	}
	kind, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_manage", true)
	if err != nil {
		return KnowledgeSettings{}, err
	}
	q := query.BindSettings(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant)).First()
	now := m.now()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if in.Version != 0 {
			return KnowledgeSettings{}, ErrConflict
		}
		row = &model.Settings{TenantID: tenant, IndexStrategy: in.IndexStrategy, SourcePermission: in.SourcePermission, MemoryApproval: in.MemoryApproval, CandidateLimit: in.CandidateLimit, Version: 1, UpdatedBy: authn.PrincipalIDFromContext(ctx), UpdatedAt: now}
		// The tenant PK is the final create-race fence after current-principal IAM locks.
		inserted := q.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).UnderlyingDB().Create(row)
		if inserted.Error != nil {
			return KnowledgeSettings{}, inserted.Error
		}
		if inserted.RowsAffected != 1 {
			return KnowledgeSettings{}, ErrConflict
		}
	} else if err != nil {
		return KnowledgeSettings{}, err
	} else {
		if row.Version != in.Version {
			return KnowledgeSettings{}, ErrConflict
		}
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Version.Eq(in.Version)).Updates(map[string]any{"index_strategy": in.IndexStrategy, "source_permission": in.SourcePermission, "memory_approval": in.MemoryApproval, "candidate_limit": in.CandidateLimit, "version": in.Version + 1, "updated_at": now, "updated_by": authn.PrincipalIDFromContext(ctx)})
		if e != nil {
			return KnowledgeSettings{}, e
		}
		if result.RowsAffected != 1 {
			return KnowledgeSettings{}, ErrConflict
		}
		row.Version++
	}
	if err = m.recordLifecycle(ctx, tx, tenant, 0, tenant, "knowledge_settings", row.Version, kind, "knowledge_manage", "knowledge.settings_updated", map[string]any{"candidate_limit": in.CandidateLimit, "index_strategy": in.IndexStrategy, "source_permission": in.SourcePermission, "memory_approval": in.MemoryApproval}); err != nil {
		return KnowledgeSettings{}, err
	}
	return m.settingsValueOn(ctx, tx, tenant)
}

type SourceInput struct {
	Scope
	SourceKind string `json:"source_kind" enum:"UPLOAD,URL,REPOSITORY"`
	Title      string `json:"title" minLength:"1" maxLength:"200"`
	Locator    string `json:"locator" minLength:"1" maxLength:"512"`
	Format     string `json:"format" enum:"MARKDOWN,TEXT,CODE,PDF"`
	Content    string `json:"content" maxLength:"2000000" doc:"Uploaded UTF-8 text only. URL/repository bindings fetch only through configured outbound connectors during synchronization."`
}
type SourceSyncInput struct {
	Version int     `json:"version" minimum:"1"`
	Content *string `json:"content,omitempty" maxLength:"2000000" doc:"Optional replacement uploaded text. Omit to rebuild the stored content."`
}
type SourceVersionInput struct {
	Version int `json:"version" minimum:"1"`
}
type SourceView struct {
	OriginKind     *string    `json:"origin_kind,omitempty"`
	OriginID       *guid.ID   `json:"origin_id,omitempty"`
	OriginVersion  int        `json:"origin_version,omitempty"`
	WorkItemID     *guid.ID   `json:"work_item_id,omitempty"`
	ClientAccepted bool       `json:"client_accepted,omitempty"`
	Tags           []string   `json:"tags"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
	ID             guid.ID    `json:"id"`
	Scope
	SourceKind     string     `json:"source_kind"`
	Title          string     `json:"title"`
	Locator        string     `json:"locator"`
	Format         string     `json:"format"`
	ContentHash    string     `json:"content_hash"`
	ChunkerVersion int        `json:"chunker_version"`
	State          string     `json:"state" enum:"PENDING,INDEXING,INDEXED,FAILED,INVALIDATED"`
	StateReason    string     `json:"state_reason,omitempty"`
	ChunkCount     int        `json:"chunk_count"`
	TextChars      int        `json:"text_chars"`
	Attempts       int        `json:"attempts"`
	IndexedAt      *time.Time `json:"indexed_at,omitempty"`
	Version        int        `json:"version"`
	CreatedBy      guid.ID    `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type ChunkView struct {
	ID            guid.ID `json:"id"`
	SourceID      guid.ID `json:"source_id"`
	EntryID       guid.ID `json:"entry_id"`
	Ordinal       int     `json:"ordinal"`
	Kind          string  `json:"kind"`
	HeadingPath   string  `json:"heading_path"`
	Content       string  `json:"content"`
	ContentHash   string  `json:"content_hash"`
	CharCount     int     `json:"char_count"`
	TokenEstimate int     `json:"token_estimate"`
}

func sourceScope(row *model.Source) Scope {
	return Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}
}
func sourceView(row *model.Source) SourceView {
	tags := []string{}
	_ = json.Unmarshal([]byte(row.TagsJSON), &tags)
	return SourceView{OriginKind: row.OriginKind, OriginID: row.OriginID, OriginVersion: row.OriginVersion, WorkItemID: row.WorkItemID, ClientAccepted: row.ClientAccepted, Tags: tags, ArchivedAt: row.ArchivedAt, ID: row.ID, Scope: sourceScope(row), SourceKind: row.SourceKind, Title: row.Title, Locator: row.Locator, Format: row.Format, ContentHash: row.ContentHash, ChunkerVersion: row.ChunkerVersion, State: row.State, StateReason: row.StateReason, ChunkCount: row.ChunkCount, TextChars: row.TextChars, Attempts: row.Attempts, IndexedAt: row.IndexedAt, Version: row.Version, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func (m *Module) nextSourceID() (guid.ID, error) {
	if m.ids == nil {
		return 0, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(id), nil
}
func (m *Module) bindSourceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in SourceInput) (SourceView, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Locator = strings.TrimSpace(in.Locator)
	if !validScope(in.Scope) || in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || in.Locator == "" || utf8.RuneCountInString(in.Locator) > 512 || !slices.Contains([]string{"UPLOAD", "URL", "REPOSITORY"}, in.SourceKind) || !slices.Contains([]string{"MARKDOWN", "TEXT", "CODE", "PDF"}, in.Format) || !utf8.ValidString(in.Content) || len(in.Content) > 10*1024*1024 || (in.SourceKind != "UPLOAD" && in.Content != "") {
		return SourceView{}, ErrInvalid
	}
	kind, err := m.authorize(ctx, tx, tenant, in.Scope, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if err = m.sourceWritePolicyOn(ctx, tx, tenant, in.Scope); err != nil {
		return SourceView{}, err
	}
	if in.SourceKind != "UPLOAD" {
		if err = validateRemoteURL(in.Locator); err != nil {
			return SourceView{}, err
		}
	}
	id, err := m.nextSourceID()
	if err != nil {
		return SourceView{}, err
	}
	now := m.now()
	actor := authn.PrincipalIDFromContext(ctx)
	row := model.Source{ID: id, TenantID: tenant, Scope: in.Scope.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID, SourceKind: in.SourceKind, Title: in.Title, Locator: in.Locator, Format: in.Format, Content: in.Content, ChunkerVersion: sourceChunkerVersion, State: "PENDING", Version: 1, CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now}
	if in.SourceKind != "UPLOAD" && !m.remote.supports(in.SourceKind) {
		row.State = "FAILED"
		row.StateReason = "UNSUPPORTED_SOURCE"
	} else if in.Format == "PDF" {
		row.State = "FAILED"
		row.StateReason = "PDF_EXTRACTION_UNAVAILABLE"
	}
	if err = query.BindSources(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return SourceView{}, err
	}
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(in.Scope), id, "knowledge_source", 1, kind, "knowledge_write", "knowledge.source_bound", map[string]any{"state": row.State, "source_kind": row.SourceKind, "state_reason": row.StateReason}); err != nil {
		return SourceView{}, err
	}
	return sourceView(&row), nil
}
func projectID(scope Scope) guid.ID {
	if scope.ProjectID != nil {
		return *scope.ProjectID
	}
	return 0
}

// Scope facts are fetched first; content is fetched only after the live IAM
// fence. Cross-tenant IDs and invisible scope IDs share the not-found shape.
func (m *Module) sourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string, lock bool) (*model.Source, string, error) {
	q := query.BindSources(tx, m.names)
	facts, err := q.WithContext(ctx).Select(q.ID, q.Scope, q.ProjectID, q.AgentPrincipalID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	kind, err := m.authorize(ctx, tx, tenant, sourceScope(facts), permission, permission != "knowledge_read")
	if errors.Is(err, ErrForbidden) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	// Source-backed documents must stop exposing bytes as soon as the owning
	// Agent retires, even before the asynchronous invalidation is dispatched.
	// Historical memory entries use their existing, separate read policy.
	if facts.Scope == "AGENT" {
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, *facts.AgentPrincipalID)
		if e != nil {
			return nil, "", e
		}
		if !found || agent.TenantID != tenant || agent.PrincipalID != *facts.AgentPrincipalID || agent.State != "ACTIVE" {
			return nil, "", ErrNotFound
		}
	}
	if permission != "knowledge_read" {
		if err = m.sourceWritePolicyOn(ctx, tx, tenant, sourceScope(facts)); err != nil {
			return nil, "", err
		}
	}
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id))
	if lock {
		read = read.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	row, err := read.First()
	return row, kind, err
}

func (m *Module) sourceWritePolicyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, scope Scope) error {
	settings, err := m.settingsValueOn(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if settings.SourcePermission == "MANAGERS" {
		_, err = m.authorize(ctx, tx, tenant, scope, "knowledge_manage", true)
	}
	return err
}

func (m *Module) syncSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SourceSyncInput, force bool) (SourceView, error) {
	if in.Version < 1 || (in.Content != nil && (!utf8.ValidString(*in.Content) || utf8.RuneCountInString(*in.Content) > 2000000)) {
		return SourceView{}, ErrInvalid
	}
	row, kind, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if row.Version != in.Version || row.State == "INVALIDATED" || row.State == "INDEXING" {
		return SourceView{}, ErrConflict
	}
	if in.Content != nil && row.SourceKind != "UPLOAD" {
		return SourceView{}, ErrInvalid
	}
	if in.Content != nil {
		row.Content = *in.Content
	}
	reason := ""
	var fragments []sourceFragment
	normalized := ""
	if row.SourceKind != "UPLOAD" {
		if !m.remote.supports(row.SourceKind) {
			reason = "UNSUPPORTED_SOURCE"
		} else {
			row.Content, err = m.remote.fetch(ctx, row.SourceKind, row.Locator, row.Format)
			if err != nil {
				return SourceView{}, err
			}
		}
	}
	if reason == "" && row.Format == "PDF" {
		reason = "PDF_EXTRACTION_UNAVAILABLE"
	} else if reason == "" {
		normalized = normalizeSource(row.Content, row.Format)
		fragments, reason = chunkSource(normalized, row.Format)
	}
	hash := sourceHash(normalized)
	row.Attempts++
	row.Version++
	row.UpdatedAt = m.now()
	row.UpdatedBy = authn.PrincipalIDFromContext(ctx)
	unchanged := reason == "" && row.State == "INDEXED" && row.ContentHash == hash && row.ChunkerVersion == sourceChunkerVersion && !force
	if reason != "" {
		if err = m.invalidateSourceChunksOn(ctx, tx, tenant, row, nil); err != nil {
			return SourceView{}, err
		}
		row.State = "FAILED"
		row.StateReason = reason
		row.ChunkCount = 0
		row.ContentHash = ""
		row.TextChars = 0
		row.IndexedAt = nil
	} else {
		if !unchanged {
			if err = m.replaceSourceChunksOn(ctx, tx, tenant, row, fragments); err != nil {
				return SourceView{}, err
			}
		}
		row.State = "INDEXED"
		row.StateReason = ""
		row.ContentHash = hash
		row.ChunkerVersion = sourceChunkerVersion
		row.TextChars = utf8.RuneCountInString(normalized)
		row.ChunkCount = len(fragments)
		row.IndexedAt = &row.UpdatedAt
	}
	if err = m.updateSourceOn(ctx, tx, row, in.Version); err != nil {
		return SourceView{}, err
	}
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(sourceScope(row)), id, "knowledge_source", row.Version, kind, "knowledge_write", "knowledge.source_synced", map[string]any{"state": row.State, "state_reason": row.StateReason, "chunk_count": row.ChunkCount, "content_hash": row.ContentHash, "unchanged": unchanged, "reindex": force}); err != nil {
		return SourceView{}, err
	}
	return sourceView(row), nil
}

func (m *Module) updateSourceOn(ctx context.Context, tx *gorm.DB, row *model.Source, version int) error {
	q := query.BindSources(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(version)).Updates(map[string]any{"content": row.Content, "content_hash": row.ContentHash, "chunker_version": row.ChunkerVersion, "state": row.State, "state_reason": row.StateReason, "chunk_count": row.ChunkCount, "text_chars": row.TextChars, "attempts": row.Attempts, "indexed_at": row.IndexedAt, "version": row.Version, "updated_by": row.UpdatedBy, "updated_at": row.UpdatedAt})
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func (m *Module) replaceSourceChunksOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, row *model.Source, fragments []sourceFragment) error {
	q := query.BindChunks(tx, m.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SourceID.Eq(row.ID), q.InvalidatedAt.IsNull()).Find()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	byHash := map[string]*model.Chunk{}
	for _, chunk := range old {
		byHash[chunk.ContentHash] = chunk
	}
	for ordinal, f := range fragments {
		keep[f.Hash] = true
		if chunk := byHash[f.Hash]; chunk != nil {
			if err = m.refreshCanonicalChunkTitleOn(ctx, tx, tenant, row, chunk); err != nil {
				return err
			}
			if _, err = q.WithContext(ctx).Where(q.ID.Eq(chunk.ID), q.TenantID.Eq(tenant)).Update(q.Ordinal, ordinal); err != nil {
				return err
			}
			continue
		}
		entry, err := m.writeCanonicalChunkOn(ctx, tx, tenant, row, f)
		if err != nil {
			return err
		}
		id, err := m.nextSourceID()
		if err != nil {
			return err
		}
		live := 1
		if err = q.WithContext(ctx).Create(&model.Chunk{ID: id, TenantID: tenant, SourceID: row.ID, EntryID: entry.ID, Ordinal: ordinal, Kind: f.Kind, HeadingPath: f.Heading, Content: f.Content, SearchTokens: strings.Join(lexicalTerms(f.Heading+"\n"+f.Content), " "), ContentHash: f.Hash, CharCount: f.Chars, TokenEstimate: f.Tokens, LiveSlot: &live, CreatedAt: m.now()}); err != nil {
			return err
		}
	}
	return m.invalidateSourceChunksOn(ctx, tx, tenant, row, keep)
}

func (m *Module) invalidateSourceChunksOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, row *model.Source, keep map[string]bool) error {
	q := query.BindChunks(tx, m.names)
	chunks, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SourceID.Eq(row.ID), q.InvalidatedAt.IsNull()).Find()
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if keep[chunk.ContentHash] {
			continue
		}
		entries := query.BindEntries(tx, m.names)
		entry, err := entries.WithContext(ctx).Where(entries.TenantID.Eq(tenant), entries.ID.Eq(chunk.EntryID)).First()
		if err != nil {
			return err
		}
		if entry.State == "ACTIVE" {
			result, e := entries.WithContext(ctx).Where(entries.TenantID.Eq(tenant), entries.ID.Eq(entry.ID), entries.Version.Eq(entry.Version)).Updates(map[string]any{"state": "REVOKED", "version": entry.Version + 1, "updated_at": m.now()})
			if e != nil {
				return e
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
		}
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(chunk.ID), q.InvalidatedAt.IsNull()).Updates(map[string]any{"invalidated_at": m.now(), "live_slot": nil}); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) invalidateSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int) (SourceView, error) {
	row, kind, err := m.sourceOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SourceView{}, err
	}
	if version < 1 {
		return SourceView{}, ErrInvalid
	}
	if row.Version != version || row.State == "INVALIDATED" {
		return SourceView{}, ErrConflict
	}
	if err = m.invalidateSourceChunksOn(ctx, tx, tenant, row, nil); err != nil {
		return SourceView{}, err
	}
	row.State = "INVALIDATED"
	row.StateReason = "MANUALLY_INVALIDATED"
	row.ChunkCount = 0
	row.Version++
	row.UpdatedAt = m.now()
	row.UpdatedBy = authn.PrincipalIDFromContext(ctx)
	if err = m.updateSourceOn(ctx, tx, row, version); err != nil {
		return SourceView{}, err
	}
	if err = m.recordLifecycle(ctx, tx, tenant, projectID(sourceScope(row)), id, "knowledge_source", row.Version, kind, "knowledge_write", "knowledge.source_invalidated", map[string]any{"state": row.State}); err != nil {
		return SourceView{}, err
	}
	return sourceView(row), nil
}

func (m *Module) recordLifecycle(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, subject string, version int, kind, permission, eventType string, data map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	event := audit.NewDomainEvent(ctx, tenant, eventType, subject, id)
	event.Detail["authority_source"] = "IAM:" + permission
	event.Detail["evidence_refs"] = []string{}
	for key, value := range data {
		event.Detail[key] = value
	}
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: eventType, Actor: events.Actor{Kind: kind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM:" + permission}, Subject: events.Subject{Kind: subject, ID: id}, SubjectVersion: int64(version), Data: data})
	return err
}
