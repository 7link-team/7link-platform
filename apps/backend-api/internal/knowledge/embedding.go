package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const embeddingPurpose = "knowledge.embedding_key"

type embeddingCookieKey struct{}

type EmbeddingDeploymentConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	AllowedHosts    []string `mapstructure:"allowed_hosts"`
	AllowedNetworks []string `mapstructure:"allowed_networks"`
}
type embeddingCacheItem struct {
	vector  []float64
	expires time.Time
	used    uint64
}
type embeddingBinding struct {
	sync.Mutex
	lease        dlock.TransactionLease
	sealer       secrets.Sealer
	consumeProof authn.ReauthenticationConsumer
	client       *http.Client
	cache        map[string]embeddingCacheItem
	sequence     uint64
	rate         map[guid.ID]embeddingRate
}
type embeddingRate struct {
	retryUntil       time.Time
	minute           time.Time
	requests, tokens int
}
type EmbeddingConfigInput struct {
	ReauthProof string `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
	BaseURL     string `json:"base_url" minLength:"1" maxLength:"512"`
	Model       string `json:"model" minLength:"1" maxLength:"128"`
	Dims        int    `json:"dims" enum:"384,768,1024,1536"`
	APIKey      string `json:"api_key,omitempty" maxLength:"4096" writeOnly:"true"`
	BatchSize   int    `json:"batch_size" default:"64" minimum:"1" maximum:"128"`
	RPM         int    `json:"rpm" default:"60" minimum:"1"`
	TPM         int    `json:"tpm" default:"150000" minimum:"1"`
	TimeoutMS   int    `json:"timeout_ms" default:"1500" minimum:"100" maximum:"30000"`
	Enabled     bool   `json:"enabled"`
	Version     int    `json:"version" minimum:"0"`
}
type EmbeddingProbeConfigInput struct {
	ReauthProof string `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
	BaseURL     string `json:"base_url,omitempty" maxLength:"512"`
	Model       string `json:"model,omitempty" maxLength:"128"`
	Dims        int    `json:"dims,omitempty" enum:"384,768,1024,1536"`
	APIKey      string `json:"api_key,omitempty" maxLength:"4096" writeOnly:"true"`
	BatchSize   int    `json:"batch_size,omitempty" default:"64" minimum:"1" maximum:"128"`
	RPM         int    `json:"rpm,omitempty" default:"60" minimum:"1"`
	TPM         int    `json:"tpm,omitempty" default:"150000" minimum:"1"`
	TimeoutMS   int    `json:"timeout_ms,omitempty" default:"1500" minimum:"100" maximum:"30000"`
	Enabled     bool   `json:"enabled,omitempty"`
	Version     int    `json:"version,omitempty" minimum:"0"`
}
type EmbeddingConfigView struct {
	BaseURL             string     `json:"base_url"`
	Model               string     `json:"model"`
	Dims                int        `json:"dims"`
	HasAPIKey           bool       `json:"has_api_key"`
	BatchSize           int        `json:"batch_size"`
	RPM                 int        `json:"rpm"`
	TPM                 int        `json:"tpm"`
	TimeoutMS           int        `json:"timeout_ms"`
	State               string     `json:"state"`
	ActiveModelKey      string     `json:"active_model_key"`
	PendingModelKey     string     `json:"pending_model_key"`
	CoveragePPM         int        `json:"coverage_ppm"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	DegradedUntil       *time.Time `json:"degraded_until,omitempty"`
	Version             int        `json:"version"`
}
type EmbeddingProbeResult struct {
	OK           bool    `json:"ok"`
	LatencyMS    float64 `json:"latency_ms"`
	DimsReturned int     `json:"dims_returned"`
	ErrorCode    string  `json:"error_code,omitempty"`
}
type EmbeddingBackfillResult struct {
	RetryAfter  time.Duration `json:"-"`
	Processed   int           `json:"processed"`
	Remaining   int           `json:"remaining"`
	CoveragePPM int           `json:"coverage_ppm"`
	ModelKey    string        `json:"model_key"`
	ErrorCode   string        `json:"error_code,omitempty"`
}
type embeddingWriteInput struct {
	KnowledgeTenantInput
	Cookie string `header:"Cookie"`
	Body   EmbeddingConfigInput
}
type embeddingProbeInput struct {
	KnowledgeTenantInput
	Cookie string `header:"Cookie"`
	Body   *EmbeddingProbeConfigInput
}

type embeddingBackfillInput struct {
	KnowledgeTenantInput
	Body struct {
		Limit int `json:"limit,omitempty" default:"64" minimum:"1" maximum:"128"`
	}
}

func (m *Module) initializeEmbeddingServices(s *module.Services) error {
	if !m.config.Embedding.Enabled {
		return nil
	}
	lease, err := module.Resolve[dlock.TransactionLease](s)
	if err != nil {
		return err
	}
	m.embedding.lease = lease
	sealer, err := module.Resolve[secrets.Sealer](s)
	if err != nil {
		return err
	}
	if err = sealer.RegisterPurpose(embeddingPurpose); err != nil {
		return err
	}
	for _, raw := range m.config.Embedding.AllowedNetworks {
		_, e := netip.ParsePrefix(raw)
		if e != nil {
			return ErrInvalid
		}
	}
	m.embedding.sealer = sealer
	consume, err := module.Resolve[authn.ReauthenticationConsumer](s)
	if err != nil {
		return err
	}
	if consume == nil {
		return ErrUnavailable
	}
	m.embedding.consumeProof = consume
	m.embedding.client = httpx.NewClient(30*time.Second, httpx.Policy{})
	// Embedding credentials are scoped to one exact origin; redirects are refused.
	m.embedding.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return nil
}
func embeddingView(c *model.EmbeddingConfig) EmbeddingConfigView {
	return EmbeddingConfigView{BaseURL: c.BaseURL, Model: c.Model, Dims: c.Dims, HasAPIKey: c.APIKeySealed != "", BatchSize: c.BatchSize, RPM: c.RPM, TPM: c.TPM, TimeoutMS: c.TimeoutMS, State: c.State, ActiveModelKey: c.ActiveModelKey, PendingModelKey: c.PendingModelKey, CoveragePPM: c.CoveragePPM, ConsecutiveFailures: c.ConsecutiveFailures, DegradedUntil: c.DegradedUntil, Version: c.Version}
}
func embeddingModelKey(base, model string, dims int) string {
	h := sha256.Sum256([]byte(base + "|" + model + "|" + strconv.Itoa(dims)))
	return hex.EncodeToString(h[:])[:16]
}
func (m *Module) embeddingConfigOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, lock bool) (*model.EmbeddingConfig, error) {
	q := query.BindEmbeddingConfig(tx.WithContext(ctx), m.native).Where("tenant_id = ?", tenant)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row model.EmbeddingConfig
	err := q.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &row, err
}
func (m *Module) validateEmbeddingInput(in *EmbeddingConfigInput) error {
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	u, e := url.Parse(in.BaseURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(in.BaseURL) > 512 || strings.TrimSpace(in.Model) == "" || len(in.Model) > 128 || !slices.Contains([]int{384, 768, 1024, 1536}, in.Dims) {
		return ErrInvalid
	}
	policy := httpx.Policy{}
	if policy.ValidateURL(in.BaseURL) != nil && !slices.Contains(m.config.Embedding.AllowedHosts, u.Hostname()) {
		return ErrInvalid
	}
	if in.BatchSize == 0 {
		in.BatchSize = 64
	}
	if in.RPM == 0 {
		in.RPM = 60
	}
	if in.TPM == 0 {
		in.TPM = 150000
	}
	if in.TimeoutMS == 0 {
		in.TimeoutMS = 1500
	}
	if in.BatchSize < 1 || in.BatchSize > 128 || in.RPM < 1 || in.TPM < 1 || in.TimeoutMS < 100 || in.TimeoutMS > 30000 || in.Version < 0 {
		return ErrInvalid
	}
	return nil
}
func (m *Module) embeddingManagementOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	if !m.config.Embedding.Enabled || tx.Dialector.Name() != "postgres" {
		return ErrUnavailable
	}
	_, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_manage", true)
	return err
}
func (m *Module) saveEmbeddingOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in EmbeddingConfigInput) (EmbeddingConfigView, error) {
	var out EmbeddingConfigView
	if err := m.embeddingManagementOn(ctx, tx, tenant); err != nil {
		return out, err
	}
	if err := m.validateEmbeddingInput(&in); err != nil {
		return out, err
	}
	if in.APIKey != "" {
		if m.embedding.consumeProof == nil {
			return out, ErrUnavailable
		}
		cookie, _ := ctx.Value(embeddingCookieKey{}).(string)
		if err := m.embedding.consumeProof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, in.ReauthProof, "knowledge_embedding_write", "tenant:"+tenant.String()+":embedding-config"); err != nil {
			return out, ErrForbidden
		}
	}
	if m.embedding.sealer == nil {
		return out, ErrUnavailable
	}
	row, err := m.embeddingConfigOn(ctx, tx, tenant, true)
	create := errors.Is(err, ErrNotFound)
	if err != nil && !create {
		return out, err
	}
	if create {
		if in.Version != 0 {
			return out, ErrConflict
		}
		row = &model.EmbeddingConfig{TenantID: tenant}
	} else if row.Version != in.Version {
		return out, ErrConflict
	}
	if row.APIKeySealed != "" && row.BaseURL != in.BaseURL && in.APIKey == "" {
		return out, ErrInvalid
	}
	sealed := row.APIKeySealed
	if in.APIKey != "" {
		aad, e := secrets.AssociatedData(tenant, tenant)
		if e != nil {
			return out, e
		}
		sealed, e = m.embedding.sealer.Seal(embeddingPurpose, aad, []byte(in.APIKey))
		if e != nil {
			return out, ErrUnavailable
		}
	}
	if in.Enabled && sealed == "" {
		return out, ErrInvalid
	}
	key := embeddingModelKey(in.BaseURL, in.Model, in.Dims)
	if row.ActiveModelKey != "" && row.ActiveModelKey != key && row.ActiveProviderJSON == "" && row.ActiveModelKey == embeddingModelKey(row.BaseURL, row.Model, row.Dims) {
		prior := *row
		prior.ActiveProviderJSON = ""
		encoded, e := json.Marshal(prior)
		if e != nil {
			return out, e
		}
		row.ActiveProviderJSON = string(encoded)
	}
	if row.ActiveModelKey != key {
		row.PendingModelKey = key
		row.CoveragePPM = 0
	}
	row.BaseURL, row.Model, row.Dims, row.APIKeySealed = in.BaseURL, in.Model, in.Dims, sealed
	row.BatchSize, row.RPM, row.TPM, row.TimeoutMS = in.BatchSize, in.RPM, in.TPM, in.TimeoutMS
	row.State = "DISABLED"
	if in.Enabled {
		row.State = "ACTIVE"
	}
	row.Version++
	row.ConsecutiveFailures = 0
	row.DegradedUntil = nil
	row.UpdatedBy = authn.PrincipalIDFromContext(ctx)
	row.UpdatedAt = m.now()
	q := query.BindEmbeddingConfig(tx.WithContext(ctx), m.native)
	if create {
		r := q.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
		if r.Error != nil {
			return out, r.Error
		}
		if r.RowsAffected != 1 {
			return out, ErrConflict
		}
	} else {
		r := q.Where("tenant_id = ? AND version = ?", tenant, in.Version).Select("*").Updates(row)
		if r.Error != nil {
			return out, r.Error
		}
		if r.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	if err = m.recordLifecycle(ctx, tx, tenant, 0, tenant, "knowledge_embedding", row.Version, "HUMAN", "knowledge_manage", "knowledge.embedding_configured", map[string]any{"model_key": key, "state": row.State}); err != nil {
		return out, err
	}
	if row.State == "ACTIVE" {
		if m.queue == nil {
			return out, ErrUnavailable
		}
		_, err = m.queue.Enqueue(ctx, tx, jobs.EnqueueRequest{TenantID: tenant, Kind: "knowledge.embedding_backfill", Payload: map[string]any{}, IdempotencyKey: "embedding-config:" + strconv.Itoa(row.Version), ConcurrencyKey: "embedding:" + tenant.String(), CreatedBy: row.UpdatedBy})
		if err != nil {
			return out, err
		}
	}
	return embeddingView(row), nil
}
func vectorText(v []float64) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.FormatFloat(n, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
func (m *Module) embed(ctx context.Context, c *model.EmbeddingConfig, inputs []string, cache bool) ([][]float64, string) {
	if m.embedding.sealer == nil || m.embedding.client == nil {
		return nil, "EMBEDDING_CREDENTIAL_UNAVAILABLE"
	}
	key := embeddingModelKey(c.BaseURL, c.Model, c.Dims)
	if len(inputs) == 0 || len(inputs) > 128 {
		return nil, "EMBEDDING_BATCH_INVALID"
	}
	cacheKey := ""
	if cache && len(inputs) == 1 {
		h := sha256.Sum256([]byte(inputs[0]))
		cacheKey = c.TenantID.String() + ":" + key + ":" + hex.EncodeToString(h[:])
		m.embedding.Lock()
		item, ok := m.embedding.cache[cacheKey]
		if ok && item.expires.After(m.now()) {
			m.embedding.sequence++
			item.used = m.embedding.sequence
			m.embedding.cache[cacheKey] = item
			m.embedding.Unlock()
			return [][]float64{slices.Clone(item.vector)}, ""
		}
		m.embedding.Unlock()
	}
	tokenCount := 0
	for _, text := range inputs {
		tokenCount += (len(text) + 2) / 3
	}
	m.embedding.Lock()
	if m.embedding.rate == nil {
		m.embedding.rate = map[guid.ID]embeddingRate{}
	}
	rate := m.embedding.rate[c.TenantID]
	if rate.retryUntil.After(m.now()) {
		m.embedding.Unlock()
		return nil, "EMBEDDING_RATE_LIMIT"
	}
	minute := m.now().Truncate(time.Minute)
	if !rate.minute.Equal(minute) {
		rate = embeddingRate{minute: minute}
	}
	if rate.requests >= c.RPM || rate.tokens+tokenCount > c.TPM {
		m.embedding.Unlock()
		return nil, "EMBEDDING_RATE_LIMIT"
	}
	rate.requests++
	rate.tokens += tokenCount
	m.embedding.rate[c.TenantID] = rate
	m.embedding.Unlock()
	aad, err := secrets.AssociatedData(c.TenantID, c.TenantID)
	if err != nil {
		return nil, "EMBEDDING_CREDENTIAL_UNAVAILABLE"
	}
	credential, err := m.embedding.sealer.Open(embeddingPurpose, aad, c.APIKeySealed)
	if err != nil {
		return nil, "EMBEDDING_CREDENTIAL_UNAVAILABLE"
	}
	payload, _ := json.Marshal(map[string]any{"model": c.Model, "input": inputs, "dimensions": c.Dims})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, "EMBEDDING_REQUEST_INVALID"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(credential))
	client := m.embedding.client
	if slices.Contains(m.config.Embedding.AllowedHosts, req.URL.Hostname()) && len(m.config.Embedding.AllowedNetworks) > 0 {
		policy := httpx.Policy{}
		for _, raw := range m.config.Embedding.AllowedNetworks {
			prefix, e := netip.ParsePrefix(raw)
			if e != nil {
				return nil, "EMBEDDING_NETWORK_POLICY_INVALID"
			}
			policy.Allow = append(policy.Allow, prefix)
		}
		client = httpx.NewClient(30*time.Second, policy)
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "EMBEDDING_REQUEST_FAILED"
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests {
		delay := time.Minute
		if seconds, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && seconds >= 0 {
			delay = min(time.Duration(seconds)*time.Second, 24*time.Hour)
		} else if at, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil && at.After(m.now()) {
			delay = min(at.Sub(m.now()), 24*time.Hour)
		}
		m.embedding.Lock()
		r := m.embedding.rate[c.TenantID]
		r.retryUntil = m.now().Add(delay)
		m.embedding.rate[c.TenantID] = r
		m.embedding.Unlock()
		return nil, "EMBEDDING_RATE_LIMIT"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "EMBEDDING_HTTP_ERROR"
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil || len(data) > 8<<20 {
		return nil, "EMBEDDING_RESPONSE_INVALID"
	}
	var decoded struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &decoded) != nil || len(decoded.Data) != len(inputs) {
		return nil, "EMBEDDING_RESPONSE_INVALID"
	}
	out := make([][]float64, len(inputs))
	for _, d := range decoded.Data {
		if d.Index < 0 || d.Index >= len(out) || out[d.Index] != nil {
			return nil, "EMBEDDING_RESPONSE_INVALID"
		}
		if len(d.Embedding) != c.Dims {
			return nil, "DIMS_MISMATCH"
		}
		norm := 0.0
		for _, n := range d.Embedding {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, "EMBEDDING_RESPONSE_INVALID"
			}
			norm += n * n
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, "EMBEDDING_RESPONSE_INVALID"
		}
		out[d.Index] = d.Embedding
	}
	if cacheKey != "" {
		m.embedding.Lock()
		if m.embedding.cache == nil {
			m.embedding.cache = map[string]embeddingCacheItem{}
		}
		if len(m.embedding.cache) >= 1000 {
			oldKey := ""
			oldUse := uint64(math.MaxUint64)
			for k, item := range m.embedding.cache {
				if item.used < oldUse {
					oldKey, oldUse = k, item.used
				}
			}
			delete(m.embedding.cache, oldKey)
		}
		m.embedding.sequence++
		m.embedding.cache[cacheKey] = embeddingCacheItem{vector: slices.Clone(out[0]), expires: m.now().Add(10 * time.Minute), used: m.embedding.sequence}
		m.embedding.Unlock()
	}
	return out, ""
}
func (m *Module) embeddingFailureOn(ctx context.Context, tx *gorm.DB, c *model.EmbeddingConfig, reason string) error {
	c.ConsecutiveFailures++
	delay := time.Minute * time.Duration(1<<min(c.ConsecutiveFailures-1, 3))
	m.embedding.Lock()
	retry := m.embedding.rate[c.TenantID].retryUntil
	m.embedding.Unlock()
	if reason == "EMBEDDING_RATE_LIMIT" && retry.After(m.now()) {
		delay = retry.Sub(m.now())
	}
	until := m.now().Add(delay)
	c.DegradedUntil = &until
	updates := map[string]any{"consecutive_failures": c.ConsecutiveFailures, "degraded_until": until}
	if c.ConsecutiveFailures >= 5 || reason == "DIMS_MISMATCH" {
		until = m.now().Add(15 * time.Minute)
		c.DegradedUntil = &until
		updates["state"] = "DEGRADED"
		updates["degraded_until"] = until
	}

	return query.BindEmbeddingConfig(tx.WithContext(ctx), m.native).Where("tenant_id = ? AND version = ?", c.TenantID, c.Version).Updates(updates).Error
}
func (m *Module) hybridRanksOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, text string, allowed []guid.ID) (map[guid.ID]int, string, string) {
	if !m.config.Embedding.Enabled {
		return nil, "", ""
	}
	fallback := func(reason string) (map[guid.ID]int, string, string) { return nil, "K2_FALLBACK", reason }
	if tx.Dialector.Name() != "postgres" {
		return fallback("VECTOR_REQUIRES_POSTGRESQL")
	}
	c, err := m.embeddingConfigOn(ctx, tx, tenant, false)
	if errors.Is(err, ErrNotFound) {
		return fallback("EMBEDDING_NOT_CONFIGURED")
	}
	if err != nil {
		return fallback("EMBEDDING_CONFIG_UNAVAILABLE")
	}
	if c.State == "DISABLED" {
		return nil, "", ""
	}
	if c.State == "DEGRADED" && c.DegradedUntil != nil && c.DegradedUntil.After(m.now()) {
		return fallback("EMBEDDING_DEGRADED")
	}
	c, err = activeEmbeddingConfig(c)
	if err != nil {
		return fallback("EMBEDDING_ACTIVE_CONFIG_UNAVAILABLE")
	}
	if c.CoveragePPM < 500000 || c.ActiveModelKey == "" {
		return fallback("EMBEDDING_COVERAGE_LOW")
	}
	if query.ProbeVector(tx, m.native) != nil {
		return fallback("PGVECTOR_0_8_UNAVAILABLE")
	}
	// Match content hashes before vector ranking: revoked/replaced rows never
	// borrow a previous embedding, even when backfill has not caught up yet.
	facts, err := query.VectorFacts(tx, m.native, tenant, c.ActiveModelKey)
	if err != nil {
		return fallback("VECTOR_READ_FAILED")
	}
	hashes := map[guid.ID]string{}
	for _, f := range facts {
		hashes[f.ChunkID] = f.ContentHash
	}
	q := query.BindChunks(tx, m.names)
	valid := []guid.ID{}
	for _, batch := range query.NativeIDBatches(allowed) {
		chunks, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.In(nativeIDValues(batch)...), q.InvalidatedAt.IsNull()).Find()
		if err != nil {
			return fallback("VECTOR_READ_FAILED")
		}
		for _, chunk := range chunks {
			if hashes[chunk.ID] == chunk.ContentHash {
				valid = append(valid, chunk.ID)
			}
		}
	}
	runes := []rune(text)
	if len(runes) > 1000 {
		text = string(runes[:1000])
	}
	vectors, reason := m.embed(ctx, c, []string{text}, true)
	if reason != "" {
		_ = m.embeddingFailureOn(ctx, tx, c, reason)
		return fallback(reason)
	}
	var hits []query.NativeHit
	err = tx.Transaction(func(candidateTx *gorm.DB) error {
		var e error
		hits, e = query.VectorMatches(candidateTx, m.native, tenant, c.ActiveModelKey, c.Dims, valid, vectorText(vectors[0]))
		return e
	})
	if err != nil {
		return fallback("VECTOR_QUERY_FAILED")
	}
	ranks := map[guid.ID]int{}
	for i, h := range hits {
		ranks[h.ID] = i + 1
	}
	return ranks, "K2", ""
}
func (m *Module) backfillEmbeddingsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, limit int) (EmbeddingBackfillResult, error) {
	var out EmbeddingBackfillResult
	if err := m.embeddingManagementOn(ctx, tx, tenant); err != nil {
		return out, err
	}
	if limit < 1 || limit > 128 {
		return out, ErrInvalid
	}
	c, err := m.embeddingConfigOn(ctx, tx, tenant, true)
	if err != nil {
		return out, err
	}
	if c.State == "DISABLED" || c.DegradedUntil != nil && c.DegradedUntil.After(m.now()) {
		return out, ErrConflict
	}
	if query.ProbeVector(tx, m.native) != nil {
		return out, ErrUnavailable
	}
	key := embeddingModelKey(c.BaseURL, c.Model, c.Dims)
	out.ModelKey = key
	corpus, err := m.nativeCorpusOn(ctx, tx, tenant, NativeSearchRequest{})
	if err != nil {
		return out, err
	}
	facts, err := query.VectorFacts(tx, m.native, tenant, key)
	if err != nil {
		return out, err
	}
	known := map[guid.ID]string{}
	for _, f := range facts {
		known[f.ChunkID] = f.ContentHash
	}
	pending := []*model.Chunk{}
	for _, chunk := range corpus.chunks {
		if known[chunk.ID] != chunk.ContentHash {
			pending = append(pending, chunk)
		}
	}
	slices.SortFunc(pending, func(a, b *model.Chunk) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	count := min(len(pending), limit, c.BatchSize)
	inputs := []string{}
	for _, chunk := range pending[:count] {
		r := []rune(chunk.HeadingPath + "\n" + chunk.Content)
		inputs = append(inputs, string(r[:min(2000, len(r))]))
	}
	if count > 0 {
		vectors, reason := m.embedBatch(ctx, c, inputs)
		if reason != "" {
			if err = m.embeddingFailureOn(ctx, tx, c, reason); err != nil {
				return out, err
			}
			out.ErrorCode = reason
			if c.DegradedUntil != nil {
				out.RetryAfter = c.DegradedUntil.Sub(m.now())
			}
			return out, nil
		}
		for i, chunk := range pending[:count] {
			if err = query.PutVector(tx, m.native, tenant, chunk.ID, key, chunk.ContentHash, c.Dims, vectorText(vectors[i]), m.now()); err != nil {
				return out, err
			}
		}
	}
	out.Processed = count
	total, complete, err := m.embeddingCoverageOn(ctx, tx, tenant, key)
	if err != nil {
		return out, err
	}
	out.Remaining = total - complete
	out.CoveragePPM = 1000000
	if total > 0 {
		out.CoveragePPM = complete * 1000000 / total
	}
	updates := map[string]any{"coverage_ppm": out.CoveragePPM, "consecutive_failures": 0, "degraded_until": nil, "state": "ACTIVE"}
	if out.CoveragePPM >= 990000 {
		updates["active_model_key"] = key
		updates["pending_model_key"] = ""
		updates["active_provider_json"] = ""
	}
	if err = query.BindEmbeddingConfig(tx.WithContext(ctx), m.native).Where("tenant_id = ? AND version = ?", tenant, c.Version).Updates(updates).Error; err != nil {
		return out, err
	}
	return out, nil
}

// Coverage counts the whole tenant's current indexed corpus, never just the
// current operator's accessible backfill batch. Only metadata is read here.
func (m *Module) embeddingCoverageOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, key string) (int, int, error) {
	live, err := query.EligibleChunkFacts(tx.WithContext(ctx), m.names, tenant, m.now())
	if err != nil {
		return 0, 0, err
	}
	facts, err := query.VectorFacts(tx, m.native, tenant, key)
	if err != nil {
		return 0, 0, err
	}
	hashes := map[guid.ID]string{}
	for _, f := range facts {
		hashes[f.ChunkID] = f.ContentHash
	}
	complete := 0
	for _, chunk := range live {
		if hashes[chunk.ChunkID] == chunk.ContentHash {
			complete++
		}
	}
	return len(live), complete, nil
}
func (m *Module) registerEmbeddingHTTP(api huma.API) {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.embedding-config.get", Method: http.MethodGet, Path: "/knowledge-embedding-config", Summary: "Read embedding configuration without credentials", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "manage"}, func(ctx context.Context, in *KnowledgeTenantInput) (*respx.Body[EmbeddingConfigView], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		var c *model.EmbeddingConfig
		e = db.Transaction(func(tx *gorm.DB) error {
			if err := m.embeddingManagementOn(ctx, tx, tenant); err != nil {
				return err
			}
			var err error
			c, err = m.embeddingConfigOn(ctx, tx, tenant, false)
			return err
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, embeddingView(c)), nil
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.embedding-config.put", Method: http.MethodPut, Path: "/knowledge-embedding-config", Summary: "Configure a sealed optional embedding provider", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "manage"}, func(ctx context.Context, in *embeddingWriteInput) (*respx.Body[EmbeddingConfigView], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		var out EmbeddingConfigView
		ctx = context.WithValue(ctx, embeddingCookieKey{}, in.Cookie)
		e = db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.saveEmbeddingOn(ctx, tx, tenant, in.Body)
			return err
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.embedding-probe.run", Method: http.MethodPost, Path: "/knowledge-embedding-probes", Summary: "Probe a saved or unsaved optional embedding provider", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "manage"}, func(ctx context.Context, in *embeddingProbeInput) (*respx.Body[EmbeddingProbeResult], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		ctx = context.WithValue(ctx, embeddingCookieKey{}, in.Cookie)
		var out EmbeddingProbeResult
		e = db.Transaction(func(tx *gorm.DB) error {
			var err error
			var config *EmbeddingConfigInput
			if in.Body != nil {
				value := EmbeddingConfigInput(*in.Body)
				config = &value
			}
			out, err = m.probeEmbeddingOn(ctx, tx, tenant, config)
			return err
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
	authz.Register(m.registrar, api, huma.Operation{OperationID: "knowledge.embedding-backfill.run", Method: http.MethodPost, Path: "/knowledge-embedding-backfills", Summary: "Backfill a bounded authorized batch using the pending embedding model", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.Guard{Resource: "knowledge", Verb: "manage"}, func(ctx context.Context, in *embeddingBackfillInput) (*respx.Body[EmbeddingBackfillResult], error) {
		tenant, e := guid.RequireID(in.TenantID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		db, e := m.database()
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		var out EmbeddingBackfillResult
		e = db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.backfillEmbeddingsOn(ctx, tx, tenant, in.Body.Limit)
			return err
		})
		if e != nil {
			return nil, knowledgeError(ctx, e)
		}
		return respx.OK(ctx, out), nil
	})
}

// probeEmbeddingOn never saves the supplied configuration or its credential.
func (m *Module) probeEmbeddingOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, input *EmbeddingConfigInput) (EmbeddingProbeResult, error) {
	var out EmbeddingProbeResult
	if err := m.embeddingManagementOn(ctx, tx, tenant); err != nil {
		return out, err
	}
	c, err := m.embeddingConfigOn(ctx, tx, tenant, false)
	saved := input == nil || input.BaseURL == "" && input.Model == "" && input.Dims == 0 && input.APIKey == ""
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	if saved && err != nil {
		return out, err
	}
	if !saved {
		in := *input
		if e := m.validateEmbeddingInput(&in); e != nil {
			return out, e
		}
		sealed := ""
		// A saved secret can only be reused for its original destination.
		if err == nil && c.BaseURL == in.BaseURL {
			sealed = c.APIKeySealed
		}
		if in.APIKey != "" {
			if m.embedding.consumeProof == nil || m.embedding.sealer == nil {
				return out, ErrUnavailable
			}
			cookie, _ := ctx.Value(embeddingCookieKey{}).(string)
			if e := m.embedding.consumeProof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, in.ReauthProof, "knowledge_embedding_write", "tenant:"+tenant.String()+":embedding-config"); e != nil {
				return out, ErrForbidden
			}
			aad, e := secrets.AssociatedData(tenant, tenant)
			if e != nil {
				return out, e
			}
			sealed, e = m.embedding.sealer.Seal(embeddingPurpose, aad, []byte(in.APIKey))
			if e != nil {
				return out, ErrUnavailable
			}
		}
		if sealed == "" {
			return out, ErrInvalid
		}
		c = &model.EmbeddingConfig{TenantID: tenant, BaseURL: in.BaseURL, Model: in.Model, Dims: in.Dims, APIKeySealed: sealed, BatchSize: in.BatchSize, RPM: in.RPM, TPM: in.TPM, TimeoutMS: in.TimeoutMS}
	}
	start := time.Now()
	vectors, reason := m.embed(ctx, c, []string{"knowledge embedding probe"}, false)
	out.LatencyMS = elapsedMS(start)
	out.ErrorCode = reason
	out.OK = reason == ""
	if out.OK {
		out.DimsReturned = len(vectors[0])
	} else if saved {
		return out, m.embeddingFailureOn(ctx, tx, c, reason)
	}
	return out, nil
}

// embeddingBackfillJob uses the platform queue lease plus a tenant-wide fence.
// The configuring human's current authority is checked again for every batch.
func (m *Module) embeddingBackfillJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	if !m.config.Embedding.Enabled {
		return nil
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	if m.embedding.lease == nil {
		return ErrUnavailable
	}
	lease, err := m.embedding.lease(ctx, "knowledge:embed:"+job.TenantID.String(), 120*time.Second)
	if errors.Is(err, dlock.ErrNotAcquired) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = lease.Unlock(context.WithoutCancel(ctx)) }()
	for range 4 {
		var result EmbeddingBackfillResult
		err = db.Transaction(func(tx *gorm.DB) error {
			if e := lease.FenceOn(ctx, tx); e != nil {
				return e
			}
			c, e := m.embeddingConfigOn(ctx, tx, job.TenantID, true)
			if errors.Is(e, ErrNotFound) {
				return nil
			}
			if e != nil {
				return e
			}
			if c.State == "DISABLED" || c.DegradedUntil != nil && c.DegradedUntil.After(m.now()) {
				return nil
			}
			actor := authn.WithClaims(ctx, &authn.Claims{PrincipalID: c.UpdatedBy, TenantID: job.TenantID, SubjectType: authn.SubjectTypePrincipal})
			result, e = m.backfillEmbeddingsOn(actor, tx, job.TenantID, c.BatchSize)
			return e
		})
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if result.ErrorCode != "" {
			return jobs.RetryAfter(result.RetryAfter, ErrUnavailable)
		}
		if result.Remaining == 0 || result.Processed == 0 {
			return nil
		}
	}
	return nil
}

// activeEmbeddingConfig keeps the old sealed provider bound to its own vectors
// until the pending provider reaches the promotion coverage threshold.
func activeEmbeddingConfig(c *model.EmbeddingConfig) (*model.EmbeddingConfig, error) {
	if c.ActiveModelKey == "" || c.ActiveModelKey == embeddingModelKey(c.BaseURL, c.Model, c.Dims) {
		return c, nil
	}
	var prior model.EmbeddingConfig
	if json.Unmarshal([]byte(c.ActiveProviderJSON), &prior) != nil || prior.TenantID != c.TenantID || prior.ActiveModelKey != c.ActiveModelKey || embeddingModelKey(prior.BaseURL, prior.Model, prior.Dims) != c.ActiveModelKey || prior.APIKeySealed == "" {
		return nil, ErrUnavailable
	}
	prior.Version = c.Version
	return &prior, nil
}

// Batch calls have their own timeout; query latency configuration stays intact.
func (m *Module) embedBatch(ctx context.Context, c *model.EmbeddingConfig, inputs []string) ([][]float64, string) {
	batch := *c
	batch.TimeoutMS = 30000
	return m.embed(ctx, &batch, inputs, false)
}
