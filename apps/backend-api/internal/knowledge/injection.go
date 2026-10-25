package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InjectionQuery struct {
	Title              string   `json:"title"`
	DescriptionExcerpt string   `json:"description_excerpt"`
	Criteria           []string `json:"criteria"`
	Feedback           []string `json:"feedback"`
}
type InjectionRequest struct {
	TenantID, ProjectID, AgentPrincipalID, ExecutionID, WorkItemID guid.ID
	Query                                                          InjectionQuery
	ExplicitIDs                                                    []guid.ID
	TokenBudget, TopK                                              int
}
type InjectionItem struct {
	ContextItem
	ChunkID         *guid.ID `json:"chunk_id,omitempty"`
	SourceID        *guid.ID `json:"source_id,omitempty"`
	SourceKind      string   `json:"source_kind"`
	Scope           string   `json:"scope"`
	Tokens          int      `json:"tokens"`
	ScorePPM        int      `json:"score_ppm"`
	Truncated       bool     `json:"truncated"`
	Inclusion       string   `json:"inclusion" enum:"PENDING,FULL,REFERENCE,DROPPED"`
	Cited           bool     `json:"cited"`
	CitationOutcome string   `json:"citation_outcome,omitempty" enum:"ACCEPTED,REJECTED"`
	SourceChanged   bool     `json:"source_changed"`
}
type InjectionResult struct {
	RetrievalLogID guid.ID         `json:"retrieval_log_id"`
	Mode           string          `json:"mode"`
	Items          []InjectionItem `json:"items"`
	Rules          []InjectionItem `json:"rules"`
	RulesOmitted   int             `json:"rules_omitted"`
	TokensTotal    int             `json:"tokens_total"`
	TokenBudget    int             `json:"token_budget"`
}
type InjectionWorkReader struct {
	VerifyOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
}
type InjectionWorkConnector struct {
	Bind func(InjectionWorkReader) error
}
type ResultExecutionReader struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)
}
type ResultExecutionConnector struct {
	Bind func(ResultExecutionReader) error
}
type injectionBinding struct {
	sync.RWMutex
	work    InjectionWorkReader
	results ResultExecutionReader
}

func (m *Module) provideInjection(s *module.Services) error {
	if err := module.Provide[ResultExecutionConnector](s, ResultExecutionConnector{Bind: func(v ResultExecutionReader) error {
		if v.ReadOn == nil {
			return ErrInvalid
		}
		m.injection.Lock()
		defer m.injection.Unlock()
		if m.injection.results.ReadOn != nil {
			return ErrConflict
		}
		m.injection.results = v
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[InjectionWorkConnector](s, InjectionWorkConnector{Bind: func(v InjectionWorkReader) error {
		if v.VerifyOn == nil {
			return ErrInvalid
		}
		m.injection.Lock()
		defer m.injection.Unlock()
		if m.injection.work.VerifyOn != nil {
			return ErrConflict
		}
		m.injection.work = v
		return nil
	}})
}
func (m *Module) injectionWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, work, agent guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || agent <= 0 || authn.PrincipalIDFromContext(ctx) != agent || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	m.injection.RLock()
	verify := m.injection.work.VerifyOn
	m.injection.RUnlock()
	if verify == nil {
		return ErrUnavailable
	}
	return verify(ctx, tx, tenant, project, work, agent)
}
func injectionTerms(in InjectionQuery) ([]string, error) {
	if utf8.RuneCountInString(in.Title) > 2000 || utf8.RuneCountInString(in.DescriptionExcerpt) > 2000 || len(in.Criteria) > 20 || len(in.Feedback) > 10 {
		return nil, ErrInvalid
	}
	parts := []string{in.Title}
	parts = append(parts, in.Feedback...)
	parts = append(parts, in.Criteria...)
	parts = append(parts, in.DescriptionExcerpt)
	terms := retrievalTokens(strings.Join(parts, "\n"))
	if len(terms) > 32 {
		terms = terms[:32]
	}
	return terms, nil
}

// tokenCost uses the same conservative UTF-8 byte estimate as source chunking.
func tokenCost(s string) int { return (len(s) + 2) / 3 }
func truncateTokens(s string, budget int) (string, bool) {
	if len(s) <= budget*3 {
		return s, false
	}
	end := budget * 3
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}
func nearDuplicate(a, b string) bool {
	x, y := map[string]bool{}, map[string]bool{}
	for _, v := range lexicalTerms(a) {
		x[v] = true
	}
	for _, v := range lexicalTerms(b) {
		y[v] = true
	}
	intersection := 0
	for v := range x {
		if y[v] {
			intersection++
		}
	}
	union := len(x) + len(y) - intersection
	return union > 0 && float64(intersection)/float64(union) >= .8
}
func (m *Module) buildInjectionOn(ctx context.Context, tx *gorm.DB, in InjectionRequest) (InjectionResult, error) {
	out := InjectionResult{Mode: "LEXICAL_ENTRIES", Items: []InjectionItem{}, Rules: []InjectionItem{}}
	archived, stateErr := m.archivedProjectOn(ctx, tx, in.TenantID, in.ProjectID)
	if stateErr != nil {
		return out, stateErr
	}
	if archived {
		return out, ErrForbidden
	}
	if err := m.injectionWorkOn(ctx, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.AgentPrincipalID); err != nil {
		return out, err
	}
	if in.ExecutionID <= 0 {
		return out, ErrInvalid
	}
	if in.TokenBudget == 0 {
		in.TokenBudget = 4000
	}
	if in.TopK == 0 {
		in.TopK = 8
	}
	if in.TokenBudget < 1 || in.TokenBudget > 20000 || in.TopK < 1 || in.TopK > 20 {
		return out, ErrInvalid
	}
	out.TokenBudget = in.TokenBudget
	terms, err := injectionTerms(in.Query)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	scopes := []Scope{{Scope: "TEAM"}, {Scope: "PROJECT", ProjectID: &in.ProjectID}, {Scope: "AGENT", AgentPrincipalID: &in.AgentPrincipalID}, {Scope: "AGENT", ProjectID: &in.ProjectID, AgentPrincipalID: &in.AgentPrincipalID}}
	var rows []*model.Entry
	for _, scope := range scopes {
		if _, err = m.authorize(ctx, tx, in.TenantID, scope, "knowledge_read", false); err != nil {
			return out, err
		}
		q := query.BindEntries(tx, m.names)
		read := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.Scope.Eq(scope.Scope), q.State.Eq("ACTIVE"))
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
		found, e := read.Order(q.ID).Limit(1001).Find()
		if e != nil {
			return out, e
		}
		if len(found) > 1000 {
			return out, ErrUnavailable
		}
		rows = append(rows, found...)
	}
	sourcesByEntry := map[guid.ID]*model.Source{}
	chunksByEntry := map[guid.ID]*model.Chunk{}
	if len(rows) > 0 {
		ids := make([]driver.Valuer, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		c := query.BindChunks(tx, m.names)
		chunks, e := c.WithContext(ctx).Where(c.TenantID.Eq(in.TenantID), c.EntryID.In(ids...), c.InvalidatedAt.IsNull()).Find()
		if e != nil {
			return out, e
		}
		sourceIDs := []driver.Valuer{}
		for _, chunk := range chunks {
			chunksByEntry[chunk.EntryID] = chunk
			if !slices.Contains(sourceIDs, driver.Valuer(chunk.SourceID)) {
				sourceIDs = append(sourceIDs, chunk.SourceID)
			}
		}
		if len(sourceIDs) > 0 {
			q := query.BindSources(tx, m.names)
			sources, e := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.ID.In(sourceIDs...), q.State.Eq("INDEXED"), q.ArchivedAt.IsNull()).Find()
			if e != nil {
				return out, e
			}
			byID := map[guid.ID]*model.Source{}
			for _, src := range sources {
				byID[src.ID] = src
			}
			for _, chunk := range chunks {
				if src := byID[chunk.SourceID]; src != nil {
					sourcesByEntry[chunk.EntryID] = src
				}
			}
		}
	}
	eligible := func(r *model.Entry) bool {
		if !m.memoryActive(r) {
			return false
		}
		if r.Kind == "DOCUMENT" && sourcesByEntry[r.ID] == nil {
			return false
		}
		src := sourcesByEntry[r.ID]
		return src == nil || src.SourceKind != "FEEDBACK" || src.WorkItemID == nil || *src.WorkItemID != in.WorkItemID
	}
	var rules []*model.Entry
	var corpus []string
	for _, r := range rows {
		if !eligible(r) {
			continue
		}
		if r.Kind == "RULE" && (r.Scope == "TEAM" || r.Scope == "AGENT") {
			rules = append(rules, r)
		} else if r.Kind != "RULE" {
			corpus = append(corpus, r.Title+"\n"+r.Body)
		}
	}
	// UpdatedAt tracks the human review transition, with ID as a stable tie-break.
	slices.SortFunc(rules, func(a, b *model.Entry) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	ruleBudget := 1500
	for _, r := range rules {
		cost := tokenCost(r.Body)
		if len(out.Rules) >= 20 || cost > ruleBudget {
			out.RulesOmitted++
			continue
		}
		item := InjectionItem{ContextItem: ContextItem{EntryID: r.ID, Version: r.Version, Label: fmt.Sprintf("[R%d]", len(out.Rules)+1), Title: r.Title, Body: r.Body, ContentHash: r.ContentHash}, Scope: r.Scope, SourceKind: "MEMORY", Tokens: cost, Inclusion: "PENDING"}
		out.Rules = append(out.Rules, item)
		ruleBudget -= cost
		out.TokensTotal += cost
	}
	explicit := map[guid.ID]bool{}
	if len(in.ExplicitIDs) > 50 {
		return out, ErrInvalid
	}
	if len(in.ExplicitIDs) > 0 {
		if _, err = m.contextItemsOn(ctx, tx, in.TenantID, in.ProjectID, in.ExplicitIDs); err != nil {
			return out, err
		}
		for _, id := range in.ExplicitIDs {
			explicit[id] = true
		}
	}
	lexical := newLexicalCorpus(corpus)
	maxScore := 0.0
	type ranked struct {
		row        *model.Entry
		raw, score float64
	}
	var rankedRows []ranked
	for _, r := range rows {
		if r.Kind == "RULE" || !eligible(r) {
			continue
		}
		score, _, hits := lexical.score(r.Title+"\n"+r.Body, terms)
		if hits == 0 && !explicit[r.ID] {
			continue
		}
		if explicit[r.ID] {
			score += 1000
		}
		if score > maxScore {
			maxScore = score
		}
		rankedRows = append(rankedRows, ranked{row: r, raw: score})
	}
	for i := range rankedRows {
		c := &rankedRows[i]
		age := math.Max(0, m.now().Sub(c.row.CreatedAt).Hours()/24)
		w, half := .6, 365.0
		if src := sourcesByEntry[c.row.ID]; src != nil {
			age = math.Max(0, m.now().Sub(src.UpdatedAt).Hours()/24)
			switch src.SourceKind {
			case "FEEDBACK":
				w, half = 1, 120
			case "REQUIREMENT":
				w, half = .9, math.Inf(1)
			case "DECISION":
				w, half = .85, 180
			case "SKILL":
				w, half = .8, math.Inf(1)
			case "DELIVERY":
				w, half = .7, 180
				if src.ClientAccepted {
					w += .1
				}
			}
		}
		if c.row.Kind == "EXPERIENCE" {
			w, half = .55, 90
		}
		scopeWeight := .4
		if c.row.Scope == "PROJECT" {
			scopeWeight = .7
		} else if c.row.Scope == "AGENT" {
			scopeWeight = 1
		}
		c.score = math.Round((.55*c.raw/maxScore+.15*math.Pow(.5, age/half)+.15*w+.15*scopeWeight)*1e6) / 1e6
	}
	slices.SortFunc(rankedRows, func(a, b ranked) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		if a.raw > b.raw {
			return -1
		}
		if a.raw < b.raw {
			return 1
		}
		if c := b.row.CreatedAt.Compare(a.row.CreatedAt); c != 0 {
			return c
		}
		if a.row.ID < b.row.ID {
			return -1
		}
		if a.row.ID > b.row.ID {
			return 1
		}
		return 0
	})
	budget := in.TokenBudget
	hashes := map[string]bool{}
	sourceCounts := map[guid.ID]int{}
	for _, c := range rankedRows {
		if len(out.Items) >= in.TopK || budget == 0 {
			break
		}
		r := c.row
		if hashes[r.ContentHash] || c.raw/maxScore < .05 || c.score < .3 {
			continue
		}
		duplicate := false
		for _, prev := range out.Items {
			if nearDuplicate(r.Body, prev.Body) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		item := InjectionItem{ContextItem: ContextItem{EntryID: r.ID, Version: r.Version, Title: r.Title, ContentHash: r.ContentHash}, Scope: r.Scope, SourceKind: "MEMORY", ScorePPM: int(math.Round(c.score * 1e6)), Inclusion: "PENDING"}
		if src := sourcesByEntry[r.ID]; src != nil {
			chunk := chunksByEntry[r.ID]
			// Canonical ingested feedback identifies its originating work in the locator.
			if src.SourceKind == "FEEDBACK" && src.WorkItemID != nil && *src.WorkItemID == in.WorkItemID {
				continue
			}
			if sourceCounts[src.ID] >= 3 {
				continue
			}
			item.ChunkID = &chunk.ID
			item.SourceID = &src.ID
			item.SourceKind = src.SourceKind
			sourceCounts[src.ID]++
		} else if r.Kind == "DOCUMENT" {
			continue
		}
		item.Body, item.Truncated = truncateTokens(r.Body, budget)
		item.SnapshotHash = fmt.Sprintf("%x", sha256.Sum256([]byte(item.Title+"\n"+item.Body)))
		item.Tokens = tokenCost(item.Body)
		item.Label = fmt.Sprintf("[K%d]", len(out.Items)+1)
		budget -= item.Tokens
		out.TokensTotal += item.Tokens
		hashes[r.ContentHash] = true
		out.Items = append(out.Items, item)
	}
	out.RetrievalLogID, err = m.nextSourceID()
	if err != nil {
		return out, err
	}
	for i := range out.Items {
		out.Items[i].RetrievalLogID = out.RetrievalLogID
	}
	for i := range out.Rules {
		out.Rules[i].RetrievalLogID = out.RetrievalLogID
	}
	body, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	row := model.InjectionLog{ID: out.RetrievalLogID, TenantID: in.TenantID, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID, ExecutionID: in.ExecutionID, WorkItemID: in.WorkItemID, ResultJSON: string(body), CreatedAt: m.now()}
	if err = query.BindInjectionLogs(tx, m.names).WithContext(ctx).Create(&row).Error; err != nil {
		return out, err
	}
	if err = m.recordLifecycle(ctx, tx, in.TenantID, in.ProjectID, row.ID, "knowledge_injection", 1, "AGENT", "knowledge_read", "knowledge.injection_built", map[string]any{"execution_id": in.ExecutionID, "work_item_id": in.WorkItemID, "items": len(out.Items), "rules": len(out.Rules), "tokens_total": out.TokensTotal}); err != nil {
		return out, err
	}
	return out, nil
}

func (m *Module) injectionLogOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, lock bool) (*model.InjectionLog, InjectionResult, error) {
	var row model.InjectionLog
	var out InjectionResult
	q := query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": id})
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&row).Error; err != nil {
		return nil, out, err
	}
	if err := m.injectionWorkOn(ctx, tx, tenant, row.ProjectID, row.WorkItemID, row.AgentPrincipalID); err != nil {
		return nil, out, err
	}
	if err := json.Unmarshal([]byte(row.ResultJSON), &out); err != nil {
		return nil, out, err
	}
	return &row, out, nil
}
func (m *Module) validateInjectionSourceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, item InjectionItem) error {
	if item.SourceID == nil {
		return nil
	}
	source, _, err := m.sourceOn(ctx, tx, tenant, *item.SourceID, "knowledge_read", false)
	if err != nil {
		return err
	}
	if source.State != "INDEXED" || source.ArchivedAt != nil || item.ChunkID == nil {
		return ErrConflict
	}
	c := query.BindChunks(tx, m.names)
	count, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(*item.ChunkID), c.SourceID.Eq(source.ID), c.EntryID.Eq(item.EntryID), c.InvalidatedAt.IsNull(), c.LiveSlot.IsNotNull()).Count()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (m *Module) validateInjectionOn(ctx context.Context, tx *gorm.DB, tenant, project, execution guid.ID, in InjectionResult) error {
	row, stored, err := m.injectionLogOn(ctx, tx, tenant, in.RetrievalLogID, false)
	if err != nil {
		return err
	}
	if row.ProjectID != project || row.ExecutionID != execution {
		return ErrForbidden
	}
	// Compare immutable selection, ignoring subsequent source-owned receipts.
	if len(stored.Items) != len(in.Items) || len(stored.Rules) != len(in.Rules) {
		return ErrConflict
	}
	items := append(append([]InjectionItem{}, in.Rules...), in.Items...)
	for i, item := range items {
		expected := append(append([]InjectionItem{}, stored.Rules...), stored.Items...)[i]
		if item.ContextItem != expected.ContextItem {
			return ErrConflict
		}
		if err := m.validateInjectionSourceOn(ctx, tx, tenant, expected); err != nil {
			return err
		}
		q := query.BindEntries(tx, m.names)
		entry, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(item.EntryID)).First()
		if err != nil {
			return err
		}
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: entry.Scope, ProjectID: entry.ProjectID, AgentPrincipalID: entry.AgentPrincipalID}, "knowledge_read", false); err != nil {
			return err
		}
		if !m.memoryActive(entry) || entry.ContentHash != item.ContentHash || entry.Version != item.Version {
			return ErrConflict
		}
	}
	return nil
}

// recordInjectionOn is called only after the runtime has verified and persisted
// its immutable plan/evidence. No public endpoint accepts citation booleans.
func (m *Module) recordInjectionOn(ctx context.Context, tx *gorm.DB, tenant, id, execution guid.ID, kind, text string) error {
	row, out, err := m.injectionLogOn(ctx, tx, tenant, id, true)
	if err != nil {
		return err
	}
	if row.ExecutionID != execution {
		return ErrForbidden
	}
	if kind != "INCLUSION" && kind != "CITATION" {
		return ErrInvalid
	}
	lists := [][]InjectionItem{out.Rules, out.Items}
	changed := false
	for _, items := range lists {
		for i := range items {
			item := &items[i]
			if e := m.validateInjectionSourceOn(ctx, tx, tenant, *item); e != nil {
				return e
			}
			current := query.BindEntries(tx, m.names)
			entry, e := current.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(current.TenantID.Eq(tenant), current.ID.Eq(item.EntryID)).First()
			if e != nil {
				return e
			}
			if _, e = m.authorize(ctx, tx, tenant, Scope{Scope: entry.Scope, ProjectID: entry.ProjectID, AgentPrincipalID: entry.AgentPrincipalID}, "knowledge_read", false); e != nil {
				return e
			}
			if kind == "INCLUSION" {
				if item.Inclusion != "PENDING" {
					continue
				}
				item.Inclusion = "FULL"
				changed = true
				if item.Truncated {
					item.Inclusion = "REFERENCE"
				}
				if !m.memoryActive(entry) || entry.ContentHash != item.ContentHash {
					return ErrConflict
				}
				if err = m.memoryUsageOn(ctx, tx, entry); err != nil {
					return err
				}
			} else if strings.Contains(text, item.Label) && !item.Cited {
				if item.Inclusion == "PENDING" {
					return ErrConflict
				}
				item.Cited = true
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err = query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": id}).Update("result_json", string(body)).Error; err != nil {
		return err
	}
	event := "knowledge.injection_included"
	if kind == "CITATION" {
		event = "knowledge.injection_cited"
	}
	return m.recordLifecycle(ctx, tx, tenant, row.ProjectID, row.ID, "knowledge_injection", 1, "AGENT", "knowledge_read", event, map[string]any{"execution_id": execution, "kind": kind})
}

func (m *Module) recordInjectionFactOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in UsageFact) (bool, error) {
	var row model.InjectionLog
	err := query.BindInjectionLogs(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "execution_id": in.ExecutionID}).Order("id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var out InjectionResult
	if err = json.Unmarshal([]byte(row.ResultJSON), &out); err != nil {
		return false, err
	}
	for _, item := range append(out.Rules, out.Items...) {
		if item.EntryID == in.EntryID && item.ContentHash == in.ContentHash {
			text := ""
			if in.Kind == "CITATION" {
				text = item.Label
			}
			if err = m.recordInjectionOn(ctx, tx, tenant, row.ID, in.ExecutionID, in.Kind, text); err != nil {
				return false, err
			}
			// Memory/RULE selections have no source-chunk receipt. The immutable log and
			// runtime evidence are their inclusion/citation record.
			return item.SourceID == nil, nil
		}
	}
	return false, ErrConflict
}
