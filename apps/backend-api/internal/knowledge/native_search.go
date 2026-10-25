package knowledge

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"golang.org/x/text/unicode/norm"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type NativeSearchRequest struct {
	IncludeArchivedProjects bool `json:"include_archived_projects,omitempty" doc:"Explicitly include authorized archived project history; excluded by default and from Agent context."`
	exactScope              *Scope
	Query                   string     `json:"query" minLength:"1" maxLength:"500"`
	MatchMode               string     `json:"match_mode" enum:"ALL,ANY" default:"ALL"`
	Scopes                  []string   `json:"scopes,omitempty" maxItems:"3"`
	ProjectIDs              []guid.ID  `json:"project_ids,omitempty" maxItems:"20"`
	AgentIDs                []guid.ID  `json:"agent_ids,omitempty" maxItems:"10"`
	SourceKinds             []string   `json:"source_kinds,omitempty" maxItems:"20"`
	Tags                    []string   `json:"tags,omitempty" maxItems:"10"`
	UpdatedAfter            *time.Time `json:"updated_after,omitempty"`
	UpdatedBefore           *time.Time `json:"updated_before,omitempty"`
	Offset                  int        `json:"offset" default:"0" minimum:"0"`
	Limit                   int        `json:"limit" default:"50" minimum:"1" maximum:"200"`
	Explain                 bool       `json:"explain"`
}
type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type NativeSearchHit struct {
	ProjectArchived bool    `json:"project_archived,omitempty"`
	ChunkID         guid.ID `json:"chunk_id"`
	SourceID        guid.ID `json:"source_id"`
	EntryID         guid.ID `json:"entry_id"`
	SourceKind      string  `json:"source_kind"`
	Scope
	Title       string                   `json:"title"`
	HeadingPath string                   `json:"heading_path"`
	Locator     string                   `json:"locator"`
	Snippet     string                   `json:"snippet"`
	Highlights  []Highlight              `json:"highlights"`
	ContentHash string                   `json:"content_hash"`
	FinalScore  float64                  `json:"final_score"`
	UpdatedAt   time.Time                `json:"updated_at"`
	Scores      *RetrievalDebugCandidate `json:"scores,omitempty"`
}
type NativeSearchResult struct {
	NativeSQLAvailable bool              `json:"native_sql_available"`
	RetrievalLogID     guid.ID           `json:"retrieval_log_id"`
	Mode               string            `json:"mode"`
	FallbackReason     string            `json:"fallback_reason,omitempty"`
	TotalCandidates    int               `json:"total_candidates"`
	Offset             int               `json:"offset"`
	Limit              int               `json:"limit"`
	Results            []NativeSearchHit `json:"results"`
}
type nativeSearchInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           NativeSearchRequest
}
type nativeLogInput struct {
	KnowledgeTenantInput
	LogID string `path:"log_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type NativeSearchLogInput struct {
	KnowledgeTenantInput
	Search  string `query:"search" maxLength:"500"`
	Purpose string `query:"purpose" enum:"SEARCH,DEBUG"`
	Offset  int    `query:"offset" default:"0" minimum:"0"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}
type NativeSearchLogPage struct {
	Items  []NativeSearchResult `json:"items"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
	Total  int                  `json:"total"`
}

func (m *Module) initializeNativeSearch(ctx context.Context) error {
	db, err := m.database()
	if err != nil {
		return nil //nolint:nilerr // Startup keeps unavailable routes registered when no database is configured.
	}
	if err = query.InstallNativeIndex(db.WithContext(ctx), m.names, m.native); err != nil {
		slog.WarnContext(ctx, "knowledge native full text unavailable", "dialect", db.Dialector.Name(), "reason", "NATIVE_FTS_UNAVAILABLE")
	}
	return nil
}
func (m *Module) nativeDebugOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RetrievalDebugQuery, base RetrievalDebugResult, diagnostic bool) (RetrievalDebugResult, bool, error) {
	request := NativeSearchRequest{Query: in.Query, MatchMode: in.Mode, Scopes: []string{in.Scope.Scope}, Limit: in.TopK, Explain: diagnostic}
	request.exactScope = &in.Scope
	if in.ProjectID != nil {
		request.ProjectIDs = []guid.ID{*in.ProjectID}
	}
	if in.AgentPrincipalID != nil {
		request.AgentIDs = []guid.ID{*in.AgentPrincipalID}
	}
	corpus, err := m.nativeCorpusOn(ctx, tx, tenant, request)
	if err != nil {
		return base, true, err
	}
	if len(corpus.chunks) == 0 {
		return base, false, nil
	}
	start := time.Now()
	result, err := m.nativeSearchOn(ctx, tx, tenant, request, false)
	if err != nil {
		return base, true, err
	}
	base.Engine = result.Mode
	base.Plan = RetrievalPlan{Mode: result.Mode, Expression: "current source authority -> native SQL text candidates -> optional vector RRF -> T/R/W/S -> thresholds -> top_k", NativeSQLAvailable: result.NativeSQLAvailable, NativeSQLReason: result.FallbackReason, VectorAvailable: result.Mode == "K2", VectorReason: result.FallbackReason}
	base.Scanned = len(corpus.chunks)
	base.EvaluatedAt = m.now().UTC().Truncate(time.Minute)
	for i, hit := range result.Results {
		c := RetrievalDebugCandidate{Final: hit.FinalScore}
		if hit.Scores != nil {
			c = *hit.Scores
		}
		c.Entry = entryView(corpus.entries[hit.EntryID])
		c.Rank = i + 1
		base.Candidates = append(base.Candidates, c)
	}
	base.Timings.CandidatesMS = elapsedMS(start)
	base.Timings.TotalMS = base.Timings.AuthorizationMS + base.Timings.TokenizationMS + base.Timings.CandidatesMS
	return base, true, nil
}

// listSearchOn reuses current list authorization and its stable ID cursor.
func (m *Module) listSearchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in knowledgeListInput) (KnowledgePage, error) {
	out := KnowledgePage{Items: []EntryView{}}
	scope, err := listScope(in)
	if err != nil {
		return out, err
	}
	if in.State != "ACTIVE" {
		return out, ErrInvalid
	}
	hash := scopeCursor(scope, tenant, authn.PrincipalIDFromContext(ctx), in.State+":"+in.Query)
	after, err := knowledgePosition(in.Cursor, hash)
	if err != nil {
		return out, err
	}
	request := NativeSearchRequest{Query: in.Query, MatchMode: "ALL", Scopes: []string{scope.Scope}, Limit: 200}
	request.exactScope = &scope
	if scope.ProjectID != nil {
		request.ProjectIDs = []guid.ID{*scope.ProjectID}
	}
	if scope.AgentPrincipalID != nil {
		request.AgentIDs = []guid.ID{*scope.AgentPrincipalID}
	}
	result, err := m.nativeSearchOn(ctx, tx, tenant, request, false)
	if err != nil {
		return out, err
	}
	ids := []guid.ID{}
	for _, hit := range result.Results {
		if hit.EntryID > after {
			ids = append(ids, hit.EntryID)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out.HasMore = len(ids) > in.Limit
	if out.HasMore {
		ids = ids[:in.Limit]
	}
	if len(ids) > 0 {
		q := query.BindEntries(tx, m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.In(nativeIDValues(ids)...), q.State.Eq("ACTIVE")).Order(q.ID).Find()
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			out.Items = append(out.Items, entryView(row))
		}
		if out.HasMore {
			out.NextCursor = knowledgeCursor(ids[len(ids)-1], hash)
		}
	}
	return out, nil
}

// highlightedSnippet chooses the densest rune window and merges overlapping
// bigram hits. The returned bytes are text, never server-authored markup.
func highlightedSnippet(text string, tokens []string) (string, []Highlight) {
	runes := []rune(text)
	lower := []rune{}
	mapping := []Highlight{}
	var iter norm.Iter
	iter.InitString(norm.NFKC, text)
	for !iter.Done() {
		before := iter.Pos()
		segment := iter.Next()
		after := iter.Pos()
		span := Highlight{utf8.RuneCountInString(text[:before]), utf8.RuneCountInString(text[:after])}
		for _, r := range string(segment) {
			lower = append(lower, unicode.ToLower(r))
			mapping = append(mapping, span)
		}
	}
	hits := []Highlight{}
	for _, token := range tokens {
		t := []rune(token)
		for i := 0; i+len(t) <= len(lower); i++ {
			if slices.Equal(lower[i:i+len(t)], t) {
				hits = append(hits, Highlight{mapping[i].Start, mapping[i+len(t)-1].End})
			}
		}
	}
	best, count := 0, -1
	for start := 0; start == 0 || start < len(runes) && start <= len(runes)-240; start++ {
		n := 0
		for _, h := range hits {
			if h.Start >= start && h.End <= start+240 {
				n++
			}
		}
		if n > count {
			best, count = start, n
		}
		if len(runes) <= 240 {
			break
		}
	}
	end := min(best+240, len(runes))
	ranges := []Highlight{}
	slices.SortFunc(hits, func(a, b Highlight) int { return a.Start - b.Start })
	for _, h := range hits {
		if h.Start < best || h.End > end {
			continue
		}
		h.Start -= best
		h.End -= best
		if len(ranges) > 0 && ranges[len(ranges)-1].End >= h.Start {
			ranges[len(ranges)-1].End = max(ranges[len(ranges)-1].End, h.End)
		} else {
			ranges = append(ranges, h)
		}
	}
	return string(runes[best:end]), ranges
}

type nativeCorpus struct {
	archived map[guid.ID]bool
	chunks   []*model.Chunk
	sources  map[guid.ID]*model.Source
	entries  map[guid.ID]*model.Entry
}

func (m *Module) nativeCorpusOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in NativeSearchRequest) (nativeCorpus, error) {
	out := nativeCorpus{sources: map[guid.ID]*model.Source{}, entries: map[guid.ID]*model.Entry{}, archived: map[guid.ID]bool{}}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"TEAM", "PROJECT", "AGENT"}
	}
	if entity := authn.EntityIDFromContext(ctx); entity > 0 {
		if len(in.ProjectIDs) > 0 && !slices.Equal(in.ProjectIDs, []guid.ID{entity}) {
			return out, ErrForbidden
		}
		in.ProjectIDs = []guid.ID{entity}
	}
	if len(in.Scopes) > 3 || len(in.ProjectIDs) > 20 || len(in.AgentIDs) > 10 || len(in.Tags) > 10 || len(in.SourceKinds) > 20 {
		return out, ErrInvalid
	}
	for _, s := range in.Scopes {
		if !slices.Contains([]string{"TEAM", "PROJECT", "AGENT"}, s) {
			return out, ErrInvalid
		}
	}
	for _, id := range append(slices.Clone(in.ProjectIDs), in.AgentIDs...) {
		if id <= 0 {
			return out, ErrInvalid
		}
	}
	for _, tag := range in.Tags {
		if tag == "" || utf8.RuneCountInString(tag) > 100 {
			return out, ErrInvalid
		}
	}
	if in.UpdatedAfter != nil && in.UpdatedBefore != nil && in.UpdatedAfter.After(*in.UpdatedBefore) {
		return out, ErrInvalid
	}
	q := query.BindSources(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("INDEXED"), q.ArchivedAt.IsNull(), q.Scope.In(in.Scopes...))
	if len(in.SourceKinds) > 0 {
		read = read.Where(q.SourceKind.In(in.SourceKinds...))
	}
	if in.UpdatedAfter != nil {
		read = read.Where(q.UpdatedAt.Gte(*in.UpdatedAfter))
	}
	if in.UpdatedBefore != nil {
		read = read.Where(q.UpdatedAt.Lte(*in.UpdatedBefore))
	}
	sources, err := read.Find()
	if err != nil {
		return out, err
	}
	sourceIDs := []guid.ID{}
	for _, s := range sources {
		if s.ProjectID != nil {
			archived, stateErr := m.archivedProjectOn(ctx, tx, tenant, *s.ProjectID)
			if stateErr != nil {
				return out, stateErr
			}
			if archived && !in.IncludeArchivedProjects {
				continue
			}
			out.archived[s.ID] = archived
		}
		if exact := in.exactScope; exact != nil {
			if (exact.ProjectID == nil) != (s.ProjectID == nil) || (exact.AgentPrincipalID == nil) != (s.AgentPrincipalID == nil) {
				continue
			}
			if exact.ProjectID != nil && *exact.ProjectID != *s.ProjectID {
				continue
			}
			if exact.AgentPrincipalID != nil && *exact.AgentPrincipalID != *s.AgentPrincipalID {
				continue
			}
		}
		if len(in.ProjectIDs) > 0 && s.ProjectID != nil && !slices.Contains(in.ProjectIDs, *s.ProjectID) {
			continue
		}
		if len(in.AgentIDs) > 0 && s.Scope == "AGENT" && (s.AgentPrincipalID == nil || !slices.Contains(in.AgentIDs, *s.AgentPrincipalID)) {
			continue
		}
		var tags []string
		if json.Unmarshal([]byte(s.TagsJSON), &tags) != nil {
			return out, ErrUnavailable
		}
		match := true
		for _, tag := range in.Tags {
			if !slices.Contains(tags, tag) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		current, _, e := m.sourceOn(ctx, tx, tenant, s.ID, "knowledge_read", false)
		if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		if in.Explain {
			if _, e = m.authorize(ctx, tx, tenant, Scope{Scope: s.Scope, ProjectID: s.ProjectID, AgentPrincipalID: s.AgentPrincipalID}, "knowledge_manage", true); e != nil {
				return out, e
			}
		}
		out.sources[s.ID] = current
		sourceIDs = append(sourceIDs, s.ID)
	}
	if len(sourceIDs) == 0 {
		return out, nil
	}
	c := query.BindChunks(tx, m.names)
	for _, batch := range query.NativeIDBatches(sourceIDs) {
		rows, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.SourceID.In(nativeIDValues(batch)...), c.InvalidatedAt.IsNull()).Find()
		if e != nil {
			return out, e
		}
		out.chunks = append(out.chunks, rows...)
	}
	entryIDs := []guid.ID{}
	for _, c := range out.chunks {
		entryIDs = append(entryIDs, c.EntryID)
	}
	if len(entryIDs) == 0 {
		return out, nil
	}
	e := query.BindEntries(tx, m.names)
	for _, batch := range query.NativeIDBatches(entryIDs) {
		entries, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ID.In(nativeIDValues(batch)...), e.State.Eq("ACTIVE")).Find()
		if err != nil {
			return out, err
		}
		for _, entry := range entries {
			if entry.Kind != "EXPERIENCE" || entry.ExpiresAt == nil || entry.ExpiresAt.After(m.now()) {
				out.entries[entry.ID] = entry
			}
		}
	}
	out.chunks = slices.DeleteFunc(out.chunks, func(c *model.Chunk) bool { return out.entries[c.EntryID] == nil })
	return out, nil
}
func (m *Module) nativeSearchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in NativeSearchRequest, persist bool) (NativeSearchResult, error) {
	out := NativeSearchResult{Mode: "K1", Offset: in.Offset, Limit: in.Limit, Results: []NativeSearchHit{}}
	if in.MatchMode == "" {
		in.MatchMode = "ALL"
	}
	tokens := retrievalModeTokens(in.Query, in.MatchMode)
	if tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 || strings.TrimSpace(in.Query) == "" || utf8.RuneCountInString(in.Query) > 500 || len(tokens) == 0 || len(tokens) > 50 || !slices.Contains([]string{"ALL", "ANY"}, in.MatchMode) || in.Offset < 0 || in.Limit < 1 || in.Limit > 200 {
		return out, ErrInvalid
	}
	if _, err := m.nativeRequestAuthorityOn(ctx, tx, tenant, in); err != nil {
		return out, err
	}
	corpus, err := m.nativeCorpusOn(ctx, tx, tenant, in)
	if err != nil {
		return out, err
	}
	nativeErr := query.EnsureNativeIndex(tx.WithContext(ctx), m.names, m.native)
	out.NativeSQLAvailable = nativeErr == nil
	ids := []guid.ID{}
	docs := []string{}
	byID := map[guid.ID]*model.Chunk{}
	for _, c := range corpus.chunks {
		ids = append(ids, c.ID)
		byID[c.ID] = c
		tokens := strings.Join(lexicalTerms(c.HeadingPath+"\n"+c.Content), " ")
		docs = append(docs, c.HeadingPath+"\n"+c.Content)
		if nativeErr == nil {
			if err = query.BindChunks(tx, m.names).WithContext(ctx).Where(query.BindChunks(tx, m.names).ID.Eq(c.ID)).UnderlyingDB().Update("search_tokens", tokens).Error; err != nil {
				return out, err
			}
			if err = query.SyncNativeChunk(tx, m.native, c.ID, tokens); err != nil {
				return out, err
			}
		}
	}
	hits := []query.NativeHit{}
	if nativeErr == nil {
		err = tx.Transaction(func(candidateTx *gorm.DB) error {
			var e error
			hits, e = query.NativeMatches(candidateTx.WithContext(ctx), m.names, m.native, ids, tokens, in.MatchMode, 200)
			return e
		})
		if err != nil {
			nativeErr = err
			out.NativeSQLAvailable = false
		}
	}
	if nativeErr != nil {
		out.Mode = "K1_FALLBACK"
		out.FallbackReason = "NATIVE_FTS_UNAVAILABLE_" + strings.ToUpper(tx.Dialector.Name())
		lex := newLexicalCorpus(docs)
		for _, c := range corpus.chunks {
			score, _, n := lex.score(c.HeadingPath+"\n"+c.Content, tokens)
			if n > 0 && (in.MatchMode == "ANY" || n == len(tokens)) {
				hits = append(hits, query.NativeHit{ID: c.ID, TextRaw: score})
			}
		}
		slices.SortFunc(hits, func(a, b query.NativeHit) int {
			if a.TextRaw > b.TextRaw {
				return -1
			}
			if a.TextRaw < b.TextRaw {
				return 1
			}
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
		if len(hits) > 200 {
			hits = hits[:200]
		}
	}
	vectorRanks, mode, reason := m.hybridRanksOn(ctx, tx, tenant, in.Query, ids)
	if mode != "" {
		out.Mode = mode
		if reason != "" {
			out.FallbackReason = reason
		}
	}
	if len(vectorRanks) > 0 {
		seen := map[guid.ID]bool{}
		for _, h := range hits {
			seen[h.ID] = true
		}
		for id := range vectorRanks {
			if !seen[id] {
				hits = append(hits, query.NativeHit{ID: id})
			}
		}
	}
	maxScore := 0.0
	for _, h := range hits {
		maxScore = max(maxScore, h.TextRaw)
	}
	ranked := []NativeSearchHit{}
	for i, h := range hits {
		c := byID[h.ID]
		if c == nil {
			continue
		}
		s := corpus.sources[c.SourceID]
		e := corpus.entries[c.EntryID]
		t := 0.0
		if maxScore > 0 {
			t = h.TextRaw / maxScore
		}
		if len(vectorRanks) > 0 {
			rrf := 0.0
			if h.TextRaw > 0 {
				rrf += 1 / (60 + float64(i+1))
			}
			if r := vectorRanks[h.ID]; r > 0 {
				rrf += 1 / (60 + float64(r))
			}
			t = rrf / (2.0 / 61)
		}
		age := math.Max(0, m.now().UTC().Truncate(time.Minute).Sub(e.CreatedAt).Hours()/24)
		r, w := math.Pow(.5, age/365), .6
		if e.Kind == "EXPERIENCE" {
			r, w = math.Pow(.5, age/90), .55
		}
		if e.Kind == "RULE" {
			r, w = 1, 1
		}
		scopeScore := .4
		if s.Scope == "PROJECT" {
			scopeScore = .7
		}
		if s.Scope == "AGENT" {
			scopeScore = 1
		}
		final := math.Round((.55*t+.15*r+.15*w+.15*scopeScore)*1e6) / 1e6
		if t < .05 || final < .3 {
			continue
		}
		snippet, highlights := highlightedSnippet(c.Content, tokens)
		hit := NativeSearchHit{ChunkID: c.ID, SourceID: s.ID, EntryID: e.ID, SourceKind: s.SourceKind, Scope: Scope{Scope: s.Scope, ProjectID: s.ProjectID, AgentPrincipalID: s.AgentPrincipalID}, Title: s.Title, HeadingPath: c.HeadingPath, Locator: s.Locator, Snippet: snippet, Highlights: highlights, ContentHash: c.ContentHash, FinalScore: final, UpdatedAt: s.UpdatedAt}
		hit.ProjectArchived = corpus.archived[s.ID]
		if in.Explain {
			hit.Scores = &RetrievalDebugCandidate{LexicalScore: h.TextRaw, T: t, R: r, W: w, S: scopeScore, Final: final, TextRank: i + 1}
		}
		ranked = append(ranked, hit)
	}
	slices.SortFunc(ranked, func(a, b NativeSearchHit) int {
		if a.FinalScore > b.FinalScore {
			return -1
		}
		if a.FinalScore < b.FinalScore {
			return 1
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			if a.UpdatedAt.After(b.UpdatedAt) {
				return -1
			}
			return 1
		}
		if a.ChunkID < b.ChunkID {
			return -1
		}
		if a.ChunkID > b.ChunkID {
			return 1
		}
		return 0
	})
	out.TotalCandidates = len(ranked)
	if in.Offset < len(ranked) {
		out.Results = ranked[in.Offset:min(in.Offset+in.Limit, len(ranked))]
	}
	if persist {
		if err = m.persistNativeSearchOn(ctx, tx, tenant, in, &out); err != nil {
			return out, err
		}
	}
	return out, nil
}
func (m *Module) persistNativeSearchOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in NativeSearchRequest, out *NativeSearchResult) error {
	id, err := m.nextSourceID()
	if err != nil {
		return err
	}
	out.RetrievalLogID = id
	request, err := json.Marshal(in)
	if err != nil {
		return err
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	purpose := "SEARCH"
	if in.Explain {
		purpose = "DEBUG"
	}
	row := model.SearchLog{ID: id, TenantID: tenant, ActorID: authn.PrincipalIDFromContext(ctx), Purpose: purpose, RequestJSON: string(request), ResultJSON: string(body), CreatedAt: m.now()}
	if err = query.BindSearchLogs(tx.WithContext(ctx), m.native).Create(&row).Error; err != nil {
		return err
	}
	kind, err := m.nativeRequestAuthorityOn(ctx, tx, tenant, in)
	if err != nil {
		return err
	}
	return m.recordLifecycle(ctx, tx, tenant, 0, id, "knowledge_search", 1, kind, "knowledge_read", "knowledge.searched", map[string]any{"mode": out.Mode, "purpose": purpose, "total_candidates": out.TotalCandidates, "fallback_reason": out.FallbackReason})
}
func (m *Module) nativeRequestAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in NativeSearchRequest) (string, error) {
	scope := Scope{Scope: "TEAM"}
	if len(in.ProjectIDs) > 0 {
		scope = Scope{Scope: "PROJECT", ProjectID: &in.ProjectIDs[0]}
	}
	if len(in.AgentIDs) > 0 {
		scope = Scope{Scope: "AGENT", AgentPrincipalID: &in.AgentIDs[0]}
		if len(in.ProjectIDs) > 0 {
			scope.ProjectID = &in.ProjectIDs[0]
		}
	}
	if in.exactScope != nil {
		scope = *in.exactScope
	}
	kind, err := m.authorize(ctx, tx, tenant, scope, "knowledge_read", false)
	if err != nil {
		return "", err
	}
	if in.Explain {
		if _, err = m.authorize(ctx, tx, tenant, scope, "knowledge_manage", true); err != nil {
			return "", err
		}
	}
	return kind, nil
}
func (m *Module) registerNativeSearchHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.search-log.list", Method: http.MethodGet, Path: "/knowledge-retrieval-logs", Summary: "List current authorized search receipts", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, func(ctx context.Context, in *NativeSearchLogInput) (*respx.Body[NativeSearchLogPage], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		var out NativeSearchLogPage
		e = db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.nativeSearchLogsOn(ctx, tx, tenant, *in)
			return err
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.search.create", Method: http.MethodPost, DefaultStatus: http.StatusCreated, Path: "/knowledge-searches", Summary: "Search current authorized canonical chunks and record the search", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, func(ctx context.Context, in *nativeSearchInput) (*respx.Body[NativeSearchResult], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		if m.replay == nil {
			return nil, knowledgeError(ctx, ErrUnavailable)
		}
		hash, e := idempotency.HashRequest(http.MethodPost, "/knowledge-searches", map[string]string{"tenant_id": tenant.String()}, in.Body)
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "knowledge.search.create", Value: in.IdempotencyKey, RequestHash: hash}
		if key.Validate() != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		var out NativeSearchResult
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Recheck current read authority before an idempotent response can return.
			if _, err := m.nativeRequestAuthorityOn(ctx, tx, tenant, in.Body); err != nil {
				return err
			}
			if _, err := m.nativeCorpusOn(ctx, tx, tenant, in.Body); err != nil {
				return err
			}
			result, replayed, err := m.replay.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
				value, err := m.nativeSearchOn(ctx, tx, tenant, in.Body, true)
				if err != nil {
					return idempotency.Result{}, err
				}
				body, err := json.Marshal(value)
				return idempotency.Result{StatusCode: http.StatusCreated, Body: body}, err
			})
			if err != nil {
				return err
			}
			if err = json.Unmarshal(result.Body, &out); err != nil {
				return err
			}
			if replayed {
				return m.reauthorizeNativeResultsOn(ctx, tx, tenant, out)
			}
			return nil
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.search-log.get", Method: http.MethodGet, Path: "/knowledge-retrieval-logs/{log_id}", Summary: "Read a current authorized search receipt", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "read"}, func(ctx context.Context, in *nativeLogInput) (*respx.Body[NativeSearchResult], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		id, e := guid.RequireID(in.LogID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		var out NativeSearchResult
		e = db.Transaction(func(tx *gorm.DB) error { return m.nativeSearchLogOn(ctx, tx, tenant, id, &out) })
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
}
func (m *Module) reauthorizeNativeResultsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, out NativeSearchResult) error {
	for _, hit := range out.Results {
		if hit.ProjectID != nil {
			archived, err := m.archivedProjectOn(ctx, tx, tenant, *hit.ProjectID)
			if err != nil {
				return err
			}
			if archived && !hit.ProjectArchived {
				return ErrConflict
			}
		}
		if entity := authn.EntityIDFromContext(ctx); entity > 0 && hit.ProjectID != nil && *hit.ProjectID != entity {
			return ErrNotFound
		}
		s, _, err := m.sourceOn(ctx, tx, tenant, hit.SourceID, "knowledge_read", false)
		if err != nil {
			return ErrNotFound
		}
		if s.State != "INDEXED" || s.ArchivedAt != nil {
			return ErrConflict
		}
		q := query.BindChunks(tx, m.names)
		c, err := q.WithContext(ctx).Where(q.ID.Eq(hit.ChunkID), q.TenantID.Eq(tenant), q.SourceID.Eq(hit.SourceID), q.InvalidatedAt.IsNull()).First()
		if err != nil || c.ContentHash != hit.ContentHash {
			return ErrConflict
		}
		e := query.BindEntries(tx, m.names)
		entry, err := e.WithContext(ctx).Where(e.ID.Eq(c.EntryID), e.TenantID.Eq(tenant), e.State.Eq("ACTIVE")).First()
		if err != nil || entry.Kind == "EXPERIENCE" && entry.ExpiresAt != nil && !entry.ExpiresAt.After(m.now()) {
			return ErrConflict
		}
		if hit.Scores != nil {
			if _, err = m.authorize(ctx, tx, tenant, hit.Scope, "knowledge_manage", true); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Module) nativeSearchLogOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, out *NativeSearchResult) error {
	var row model.SearchLog
	err := query.BindSearchLogs(tx.WithContext(ctx), m.native).Where(map[string]any{"id": id, "tenant_id": tenant, "actor_id": authn.PrincipalIDFromContext(ctx)}).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var request NativeSearchRequest
	if err = json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
		return err
	}
	request.Explain = false
	if _, err = m.nativeRequestAuthorityOn(ctx, tx, tenant, request); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(row.ResultJSON), out); err != nil {
		return err
	}
	for i := range out.Results {
		if out.Results[i].Scores != nil {
			if _, err = m.authorize(ctx, tx, tenant, out.Results[i].Scope, "knowledge_manage", true); errors.Is(err, ErrForbidden) {
				out.Results[i].Scores = nil
			} else if err != nil {
				return err
			}
		}
	}
	return m.reauthorizeNativeResultsOn(ctx, tx, tenant, *out)
}
func (m *Module) nativeSearchLogsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in NativeSearchLogInput) (NativeSearchLogPage, error) {
	out := NativeSearchLogPage{Items: []NativeSearchResult{}, Offset: in.Offset, Limit: in.Limit}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 || utf8.RuneCountInString(in.Search) > 500 || in.Purpose != "" && !slices.Contains([]string{"SEARCH", "DEBUG"}, in.Purpose) {
		return out, ErrInvalid
	}
	if _, err := m.nativeRequestAuthorityOn(ctx, tx, tenant, NativeSearchRequest{}); err != nil {
		return out, err
	}
	q := query.BindSearchLogs(tx.WithContext(ctx), m.native).Where("tenant_id = ? AND actor_id = ?", tenant, authn.PrincipalIDFromContext(ctx))
	if in.Purpose != "" {
		q = q.Where("purpose = ?", in.Purpose)
	}
	var rows []model.SearchLog
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		return out, err
	}
	matched := []NativeSearchResult{}
	for _, row := range rows {
		var request NativeSearchRequest
		if err := json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
			return out, err
		}
		if !strings.Contains(strings.ToLower(request.Query), strings.ToLower(in.Search)) {
			continue
		}
		var result NativeSearchResult
		err := m.nativeSearchLogOn(ctx, tx, tenant, row.ID, &result)
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return out, err
		}
		matched = append(matched, result)
	}
	out.Total = len(matched)
	if in.Offset < len(matched) {
		out.Items = matched[in.Offset:min(in.Offset+in.Limit, len(matched))]
	}
	return out, nil
}

func nativeIDValues(ids []guid.ID) []driver.Valuer {
	out := make([]driver.Valuer, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
