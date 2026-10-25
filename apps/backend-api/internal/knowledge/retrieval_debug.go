package knowledge

// Canonical retrieval diagnostics operate over the canonical knowledge entries.
// This is lexical retrieval, not a claim that an FTS or embedding index exists.
import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type RetrievalDebugQuery struct {
	Scope
	Query           string   `json:"query" minLength:"1" maxLength:"500"`
	Mode            string   `json:"mode" enum:"ALL,ANY"`
	SimulateAgentID *guid.ID `json:"simulate_agent_id,omitempty"`
	TopK            int      `json:"top_k" minimum:"1" maximum:"200"`
}

type RetrievalDebugCandidate struct {
	Entry        EntryView `json:"entry"`
	TextRaw      int       `json:"text_raw"`
	LexicalScore float64   `json:"lexical_score" doc:"BM25 score over complete lexical terms in the authorized active candidate corpus; no native FTS or vector index is implied."`
	T            float64   `json:"t"`
	R            float64   `json:"r"`
	W            float64   `json:"w"`
	S            float64   `json:"s"`
	Final        float64   `json:"final"`
	TextRank     int       `json:"text_rank"`
	Rank         int       `json:"rank"`
	Exclusion    string    `json:"exclusion,omitempty"`
}

type RetrievalDebugResult struct {
	LogID       guid.ID                   `json:"log_id"`
	Dialect     string                    `json:"dialect"`
	Plan        RetrievalPlan             `json:"plan"`
	Timings     RetrievalTimings          `json:"timings"`
	Query       RetrievalDebugQuery       `json:"query"`
	ActorID     guid.ID                   `json:"actor_id"`
	Engine      string                    `json:"engine" enum:"LEXICAL_ENTRIES,K1,K1_FALLBACK,K2,K2_FALLBACK"`
	Tokens      []string                  `json:"tokens"`
	Candidates  []RetrievalDebugCandidate `json:"candidates"`
	Scanned     int                       `json:"scanned"`
	Truncated   bool                      `json:"truncated" doc:"True when more than 1000 authorized entries exist; only the newest 1000 were evaluated."`
	EvaluatedAt time.Time                 `json:"evaluated_at"`
}

type retrievalDebugInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           RetrievalDebugQuery
}

func retrievalTokens(text string) []string {
	words := lexicalTokenize(text, true)
	var tokens []string
	for _, word := range words {
		if !slices.Contains(tokens, word) {
			tokens = append(tokens, word)
		}
	}
	return tokens
}
func retrievalModeTokens(text, mode string) []string {
	tokens := retrievalTokens(text)
	limit := 16
	if mode == "ANY" {
		limit = 32
	}
	return tokens[:min(limit, len(tokens))]
}

func (m *Module) authorizeDebugOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, s Scope) error {
	// Management permits diagnostics, but never expands the underlying read set.
	if _, err := m.authorize(ctx, tx, tenant, s, "knowledge_manage", true); err != nil {
		return err
	}
	_, err := m.authorize(ctx, tx, tenant, s, "knowledge_read", true)
	return err
}

func (m *Module) retrieveDebugOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RetrievalDebugQuery) (RetrievalDebugResult, error) {
	return m.retrieveLexicalOn(ctx, tx, tenant, in, true)
}

func (m *Module) retrieveLexicalOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RetrievalDebugQuery, diagnostic bool) (RetrievalDebugResult, error) {
	started := time.Now()
	tokenStart := time.Now()
	out := RetrievalDebugResult{Query: in, Engine: "LEXICAL_ENTRIES", ActorID: authn.PrincipalIDFromContext(ctx), Tokens: retrievalModeTokens(in.Query, in.Mode), Candidates: []RetrievalDebugCandidate{}}
	out.Timings.TokenizationMS = elapsedMS(tokenStart)
	if tx == nil || tx.Statement == nil || tx.Dialector == nil {
		return out, ErrInvalid
	}
	out.Dialect = tx.Dialector.Name()
	out.Plan = lexicalRetrievalPlan(in.Mode)
	if !validScope(in.Scope) || strings.TrimSpace(in.Query) == "" || utf8.RuneCountInString(in.Query) > 500 || len(out.Tokens) == 0 || len(out.Tokens) > 50 || (in.Mode != "ALL" && in.Mode != "ANY") || in.TopK < 1 || in.TopK > 200 {
		return out, ErrInvalid
	}
	authorizationStart := time.Now()
	if diagnostic {
		if err := m.authorizeDebugPerspectiveOn(ctx, tx, tenant, in); err != nil {
			return out, err
		}
	} else if _, err := m.authorize(ctx, tx, tenant, in.Scope, "knowledge_read", false); err != nil {
		return out, err
	}
	out.Timings.AuthorizationMS = elapsedMS(authorizationStart)
	if native, used, err := m.nativeDebugOn(ctx, tx, tenant, in, out, diagnostic); used || err != nil {
		return native, err
	}
	settings, err := m.settingsValueOn(ctx, tx, tenant)
	if err != nil {
		return out, err
	}
	if m.now == nil {
		return out, ErrUnavailable
	}
	evaluationNow := m.now().UTC()
	out.EvaluatedAt = evaluationNow.Truncate(time.Minute)
	candidateStart := time.Now()
	q := query.BindEntries(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Scope.Eq(in.Scope.Scope))
	if in.ProjectID == nil {
		read = read.Where(q.ProjectID.IsNull())
	} else {
		read = read.Where(q.ProjectID.Eq(*in.ProjectID))
	}
	if in.AgentPrincipalID == nil {
		read = read.Where(q.AgentPrincipalID.IsNull())
	} else {
		read = read.Where(q.AgentPrincipalID.Eq(*in.AgentPrincipalID))
	}
	rows, err := read.Order(q.ID.Desc()).Limit(1001).Find()
	if err != nil {
		return out, err
	}
	out.Timings.CandidatesMS = elapsedMS(candidateStart)
	rankStart := time.Now()
	out.Truncated = len(rows) > 1000
	if out.Truncated {
		rows = rows[:1000]
	}
	out.Scanned = len(rows)
	corpus := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.State == "ACTIVE" && !(row.Kind == "EXPERIENCE" && row.ExpiresAt != nil && !row.ExpiresAt.After(evaluationNow)) {
			corpus = append(corpus, row.Title+"\n"+row.Body)
		}
	}
	lexical := newLexicalCorpus(corpus)
	maxScore := 0.0
	for _, row := range rows {
		c := RetrievalDebugCandidate{Entry: entryView(row)}
		if row.State != "ACTIVE" {
			c.Exclusion = "INACTIVE"
		} else if row.Kind == "EXPERIENCE" && row.ExpiresAt != nil && !row.ExpiresAt.After(evaluationNow) {
			c.Exclusion = "EXPIRED"
		} else {
			hits := 0
			c.LexicalScore, c.TextRaw, hits = lexical.score(row.Title+"\n"+row.Body, out.Tokens)
			if hits == 0 || (in.Mode == "ALL" && hits != len(out.Tokens)) {
				c.Exclusion = "QUERY_MISMATCH"
			} else if c.LexicalScore > maxScore {
				maxScore = c.LexicalScore
			}
		}
		out.Candidates = append(out.Candidates, c)
	}
	slices.SortFunc(out.Candidates, func(a, b RetrievalDebugCandidate) int {
		if a.LexicalScore != b.LexicalScore {
			if a.LexicalScore > b.LexicalScore {
				return -1
			}
			return 1
		}
		if a.Entry.ID < b.Entry.ID {
			return -1
		}
		if a.Entry.ID > b.Entry.ID {
			return 1
		}
		return 0
	})
	textRank := 0
	for i := range out.Candidates {
		c := &out.Candidates[i]
		if c.Exclusion != "" {
			continue
		}
		textRank++
		c.TextRank = textRank
		if textRank > settings.CandidateLimit {
			c.Exclusion = "CANDIDATE_LIMIT"
			continue
		}
		c.T = c.LexicalScore / maxScore
		age := math.Max(0, out.EvaluatedAt.Sub(c.Entry.CreatedAt).Hours()/24)
		c.W, c.R = .6, math.Pow(.5, age/365)
		if c.Entry.Kind == "EXPERIENCE" {
			c.W, c.R = .55, math.Pow(.5, age/90)
		}
		if c.Entry.Kind == "RULE" {
			c.W, c.R = 1, 1
		}
		c.S = .4
		if in.Scope.Scope == "PROJECT" {
			c.S = .7
		}
		if in.Scope.Scope == "AGENT" {
			c.S = 1
		}
		c.Final = math.Round((.55*c.T+.15*c.R+.15*c.W+.15*c.S)*1e6) / 1e6
		if c.T < .05 {
			c.Exclusion = "TEXT_THRESHOLD"
		} else if c.Final < .30 {
			c.Exclusion = "FINAL_THRESHOLD"
		}
	}
	slices.SortFunc(out.Candidates, func(a, b RetrievalDebugCandidate) int {
		if (a.Exclusion == "") != (b.Exclusion == "") {
			if a.Exclusion == "" {
				return -1
			}
			return 1
		}
		if a.Final != b.Final {
			if a.Final > b.Final {
				return -1
			}
			return 1
		}
		if a.T != b.T {
			if a.T > b.T {
				return -1
			}
			return 1
		}
		if !a.Entry.CreatedAt.Equal(b.Entry.CreatedAt) {
			if a.Entry.CreatedAt.After(b.Entry.CreatedAt) {
				return -1
			}
			return 1
		}
		if a.Entry.ID < b.Entry.ID {
			return -1
		}
		if a.Entry.ID > b.Entry.ID {
			return 1
		}
		return 0
	})
	rank := 0
	for i := range out.Candidates {
		c := &out.Candidates[i]
		if c.Exclusion == "" {
			rank++
			if rank > in.TopK {
				c.Exclusion = "TOP_K"
			} else {
				c.Rank = rank
			}
		}
	}
	// Include excluded evidence without returning an unbounded response.
	if len(out.Candidates) > 200 {
		out.Candidates = out.Candidates[:200]
	}
	out.Timings.RankingMS = elapsedMS(rankStart)
	out.Timings.TotalMS = elapsedMS(started)
	return out, nil
}

func (m *Module) registerRetrievalDebugHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.retrieval.debug", Method: http.MethodPost, Path: "/knowledge/retrieval-debug", Summary: "Diagnose actual scoped lexical knowledge retrieval", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "manage"}, m.retrievalDebugHTTP)
}

func (m *Module) retrievalDebugHTTP(ctx context.Context, in *retrievalDebugInput) (*respx.Body[RetrievalDebugResult], error) {
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, knowledgeError(ctx, ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	if m.replay == nil || m.audit == nil {
		return nil, knowledgeError(ctx, ErrUnavailable)
	}
	hash, err := idempotency.HashRequest(http.MethodPost, "/knowledge/retrieval-debug", map[string]string{"tenant_id": tenant.String()}, in.Body)
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	key := idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "knowledge.retrieval.debug", Value: in.IdempotencyKey, RequestHash: hash}
	if err = key.Validate(); err != nil {
		return nil, knowledgeError(ctx, err)
	}
	var out RetrievalDebugResult
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.authorizeDebugPerspectiveOn(ctx, tx, tenant, in.Body); e != nil {
			return e
		}
		result, replayed, e := m.replay.Do(ctx, tx, key, func(tx *gorm.DB) (idempotency.Result, error) {
			value, e := m.retrieveDebugOn(ctx, tx, tenant, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			if e = m.persistDiagnosticOn(ctx, tx, tenant, &value); e != nil {
				return idempotency.Result{}, e
			}
			event := audit.NewDomainEvent(ctx, tenant, "knowledge.retrieval_debugged", "knowledge_retrieval", 0)
			event.Detail["authority_source"], event.Detail["purpose"] = "ROLE", "DEBUG"
			event.Detail["scope"], event.Detail["scanned"], event.Detail["engine"] = in.Body.Scope, value.Scanned, value.Engine
			if e = m.audit(ctx, tx, event); e != nil {
				return idempotency.Result{}, e
			}
			body, e := json.Marshal(value)
			return idempotency.Result{StatusCode: 200, Body: body}, e
		})
		if e != nil {
			return e
		}
		if e = json.Unmarshal(result.Body, &out); e != nil {
			return e
		}
		if replayed {
			// Never replay content that has since been revoked or left the read set.
			fresh, e := m.retrieveDebugOn(ctx, tx, tenant, in.Body)
			if e != nil {
				return e
			}
			if !sameDebugEntries(out, fresh) {
				return ErrConflict
			}
		}
		return nil
	})
	if err != nil {
		return nil, knowledgeError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func sameDebugEntries(a, b RetrievalDebugResult) bool {
	if len(a.Candidates) != len(b.Candidates) {
		return false
	}
	for i := range a.Candidates {
		x, y := a.Candidates[i].Entry, b.Candidates[i].Entry
		if x.ID != y.ID || x.Version != y.Version || x.State != y.State || x.ContentHash != y.ContentHash {
			return false
		}
	}
	return true
}
