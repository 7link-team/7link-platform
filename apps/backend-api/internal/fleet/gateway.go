package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GatewayProvider struct {
	ID      string   `json:"id" maxLength:"64"`
	BaseURL string   `json:"base_url" maxLength:"1024"`
	KeyRef  *guid.ID `json:"key_ref,omitempty"`
}
type GatewayModel struct {
	ID               string `json:"id" maxLength:"160"`
	Provider         string `json:"provider" maxLength:"64"`
	Runtime          string `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	InputMicros      int64  `json:"input_micros" minimum:"0" maximum:"9007199254740991"`
	OutputMicros     int64  `json:"output_micros" minimum:"0" maximum:"9007199254740991"`
	CacheReadMicros  int64  `json:"cache_read_micros" minimum:"0" maximum:"9007199254740991"`
	CacheWriteMicros int64  `json:"cache_write_micros" minimum:"0" maximum:"9007199254740991"`
}
type GatewayBudget struct {
	ProjectID   guid.ID `json:"project_id"`
	LimitMicros int64   `json:"limit_micros" minimum:"0" maximum:"9007199254740991"`
}
type GatewayRecords struct {
	KeyRef    *guid.ID          `json:"key_ref,omitempty"`
	Providers []GatewayProvider `json:"providers" maxItems:"50"`
	Models    []GatewayModel    `json:"models" maxItems:"1000"`
	Budgets   []GatewayBudget   `json:"budgets" maxItems:"1000"`
}
type GatewayWrite struct {
	Enabled      bool     `json:"enabled"`
	MachineID    *guid.ID `json:"machine_id,omitempty"`
	DefaultModel string   `json:"default_model" maxLength:"160"`
	GatewayRecords
}
type GatewayView struct {
	GatewayWrite
	Version   int    `json:"version"`
	Revision  int64  `json:"revision"`
	Available bool   `json:"available"`
	Reason    string `json:"reason" enum:"PROVIDER_CONFIGURED,RUNNER_GATEWAY_UNAVAILABLE"`
}
type GatewayKeyView struct {
	ID        guid.ID   `json:"id"`
	Last4     string    `json:"last4"`
	CreatedAt time.Time `json:"created_at"`
}

type GatewayRotationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)

// Gateway notifications consume actual persisted revocation/replacement facts.
func (m *Module) gatewayRotationFactOn(ctx context.Context, tx *gorm.DB, tenant, replacement guid.ID) (bool, error) {
	if err := fleetTransaction(tx, tenant, replacement); err != nil {
		return false, err
	}
	q := query.BindGatewayKeys(tx, m.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ReplacementID.Eq(replacement), q.RevokedAt.IsNotNull()).Count()
	if err != nil || n == 0 {
		return false, err
	}
	n, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(replacement), q.RevokedAt.IsNull()).Count()
	return n == 1, err
}

type GatewayRequestView struct {
	ID         guid.ID    `json:"id"`
	ProjectID  *guid.ID   `json:"project_id,omitempty"`
	RequestID  string     `json:"request_id"`
	Revision   int64      `json:"revision"`
	Model      string     `json:"model"`
	State      string     `json:"state" enum:"QUEUED,RUNNING,COMPLETED,FAILED,CANCELLED"` //nolint:misspell // Frozen protocol spelling.
	Reason     string     `json:"reason"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func gatewayRequestView(r *model.GatewayRequest) GatewayRequestView {
	return GatewayRequestView{ID: r.ID, ProjectID: r.ProjectID, RequestID: "gwr_" + r.ID.String(), Revision: r.Revision, Model: r.Model, State: r.State, Reason: r.Reason, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}
func emptyGateway() GatewayView {
	return GatewayView{GatewayWrite: GatewayWrite{GatewayRecords: GatewayRecords{Providers: []GatewayProvider{}, Models: []GatewayModel{}, Budgets: []GatewayBudget{}}}, Reason: "RUNNER_GATEWAY_UNAVAILABLE"}
}
func (m *Module) gatewayOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, lock bool) (*model.GatewayConfig, error) {
	q := query.BindGatewayConfigs(tx, m.names)
	query := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return query.First()
}
func gatewayView(row *model.GatewayConfig) (GatewayView, error) {
	v := emptyGateway()
	v.Enabled = row.Enabled
	v.MachineID = row.MachineID
	v.DefaultModel = row.DefaultModel
	v.Version = row.Version
	v.Revision = row.Revision
	if json.Unmarshal([]byte(row.RecordsJSON), &v.GatewayRecords) != nil {
		return v, ErrUnavailable
	}
	for _, selected := range v.Models {
		if v.Enabled && selected.ID == v.DefaultModel {
			for _, provider := range v.Providers {
				if provider.ID == selected.Provider && provider.BaseURL != "" {
					v.Available, v.Reason = true, "PROVIDER_CONFIGURED"
				}
			}
		}
	}
	return v, nil
}
func (m *Module) GatewayOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (GatewayView, error) {
	if err := fleetTransaction(tx, tenant, tenant); err != nil {
		return GatewayView{}, err
	}
	if m == nil || m.ids == nil {
		return GatewayView{}, ErrUnavailable
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return GatewayView{}, err
	}
	r, err := m.gatewayOn(ctx, tx, tenant, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return emptyGateway(), nil
	}
	if err != nil {
		return GatewayView{}, err
	}
	return gatewayView(r)
}
func (m *Module) gatewayEventOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string, version int64) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, tenant, "fleet."+kind, "model_gateway", id)
	e.Detail["version"] = version
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: kind, Subject: events.Subject{Kind: "model_gateway", ID: id}, SubjectVersion: version, Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"version": version}})
	return err
}
func (m *Module) validateGatewayOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in GatewayWrite) error {
	if len(in.Providers) > 50 || len(in.Models) > 1000 || len(in.Budgets) > 1000 {
		return ErrInvalidArgument
	}
	if in.KeyRef != nil {
		q := query.BindGatewayKeys(tx, m.names)
		if *in.KeyRef <= 0 {
			return ErrInvalidArgument
		}
		if _, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*in.KeyRef), q.RevokedAt.IsNull()).First(); e != nil {
			return ErrInvalidArgument
		}
	}
	providers := map[string]bool{}
	models := map[string]bool{}
	budgets := map[guid.ID]bool{}
	for _, p := range in.Providers {
		u, e := url.Parse(p.BaseURL)
		if !discoveryID(p.ID, 64) || providers[p.ID] || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(p.BaseURL) > 1024 {
			return ErrInvalidArgument
		}
		providers[p.ID] = true
		if p.KeyRef != nil {
			q := query.BindGatewayKeys(tx, m.names)
			if *p.KeyRef <= 0 {
				return ErrInvalidArgument
			}
			if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*p.KeyRef), q.RevokedAt.IsNull()).First(); e != nil {
				return ErrInvalidArgument
			}
		}
	}
	for _, v := range in.Models {
		if !discoveryID(v.ID, 160) || models[v.ID] || !providers[v.Provider] || !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, v.Runtime) {
			return ErrInvalidArgument
		}
		models[v.ID] = true
		for _, p := range []int64{v.InputMicros, v.OutputMicros, v.CacheReadMicros, v.CacheWriteMicros} {
			if p < 0 || p > 9007199254740991 {
				return ErrInvalidArgument
			}
		}
	}
	for _, v := range in.Budgets {
		if v.ProjectID <= 0 || budgets[v.ProjectID] || v.LimitMicros < 0 || v.LimitMicros > 9007199254740991 {
			return ErrInvalidArgument
		}
		budgets[v.ProjectID] = true
	}
	if in.DefaultModel != "" && !models[in.DefaultModel] {
		return ErrInvalidArgument
	}
	if in.MachineID != nil {
		if *in.MachineID <= 0 {
			return ErrInvalidArgument
		}
		if _, e := m.machineOn(ctx, tx, tenant, *in.MachineID, false); e != nil {
			return e
		}
	}
	if in.Enabled {
		if !models[in.DefaultModel] {
			return ErrInvalidArgument
		}
	}
	return nil
}

// RotateGatewayKeyOn replaces every current configuration reference and revokes
// the previous sealed key atomically. No runtime installation is inferred.
func (m *Module) RotateGatewayKeyOn(ctx context.Context, tx *gorm.DB, tenant, oldID guid.ID, version int, reason string) (GatewayKeyView, error) {
	var empty GatewayKeyView
	if err := fleetTransaction(tx, tenant, oldID); err != nil {
		return empty, err
	}
	if version < 1 || !utf8.ValidString(reason) || utf8.RuneCountInString(strings.TrimSpace(reason)) < 10 || utf8.RuneCountInString(reason) > 2000 {
		return empty, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return empty, err
	}
	config, err := m.gatewayOn(ctx, tx, tenant, true)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrVersionConflict
	}
	if err != nil {
		return empty, err
	}
	if config.Version != version {
		return empty, ErrVersionConflict
	}
	keys := query.BindGatewayKeys(tx, m.names)
	old, err := keys.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(keys.TenantID.Eq(tenant), keys.ID.Eq(oldID), keys.RevokedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrVersionConflict
	}
	if err != nil {
		return empty, err
	}
	// Imported provider credentials must be replaced at their provider and
	// re-imported; generating random bytes here would destroy a valid route.
	plain, err := m.openGatewayKey(tenant, old)
	if err != nil {
		return empty, ErrUnavailable
	}
	digest := sha256.Sum256(plain)
	clear(plain)
	if old.KeyHash != hex.EncodeToString(digest[:]) {
		return empty, ErrInvalidArgument
	}
	view, err := gatewayView(config)
	if err != nil {
		return empty, err
	}
	replacement, err := m.CreateGatewayKeyOn(ctx, tx, tenant)
	if err != nil {
		return empty, err
	}
	if view.KeyRef != nil && *view.KeyRef == oldID {
		view.KeyRef = &replacement.ID
	}
	for i := range view.Providers {
		if view.Providers[i].KeyRef != nil && *view.Providers[i].KeyRef == oldID {
			view.Providers[i].KeyRef = &replacement.ID
		}
	}
	if _, err = m.SaveGatewayOn(ctx, tx, tenant, version, view.GatewayWrite); err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	changed, err := keys.WithContext(ctx).Where(keys.TenantID.Eq(tenant), keys.ID.Eq(old.ID), keys.RevokedAt.IsNull()).Updates(map[string]any{"revoked_at": now, "replacement_id": replacement.ID, "sealed": ""})
	if err != nil {
		return empty, err
	}
	if changed.RowsAffected != 1 {
		return empty, ErrVersionConflict
	}
	e := audit.NewDomainEvent(ctx, tenant, "fleet.gateway.key_rotated", "gateway_key", replacement.ID)
	e.Detail["old_key_id"], e.Detail["reason"] = oldID.String(), reason
	if err = m.audit(ctx, tx, e); err != nil {
		return empty, err
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "gateway.key_rotated", Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Subject: events.Subject{Kind: "gateway_key", ID: replacement.ID}, SubjectVersion: 1, Data: map[string]any{"old_key_id": oldID, "new_key_id": replacement.ID, "config_version": version + 1}})
	return replacement, err
}
func (m *Module) SaveGatewayOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, version int, in GatewayWrite) (GatewayView, error) {
	if err := fleetTransaction(tx, tenant, tenant); err != nil {
		return GatewayView{}, err
	}
	if m == nil || m.ids == nil {
		return GatewayView{}, ErrUnavailable
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return GatewayView{}, err
	}
	row, err := m.gatewayOn(ctx, tx, tenant, true)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return GatewayView{}, err
	}
	if row == nil {
		if version != 0 {
			return GatewayView{}, ErrVersionConflict
		}
		id, e := m.ids.Next()
		if e != nil {
			return GatewayView{}, e
		}
		if id <= 0 {
			return GatewayView{}, ErrUnavailable
		}
		row = &model.GatewayConfig{ID: guid.ID(id), TenantID: tenant}
	} else if row.Version != version {
		return GatewayView{}, ErrVersionConflict
	}
	if err = m.validateGatewayOn(ctx, tx, tenant, in); err != nil {
		return GatewayView{}, err
	}
	b, _ := json.Marshal(in.GatewayRecords)
	row.Enabled = in.Enabled
	row.MachineID = in.MachineID
	row.DefaultModel = in.DefaultModel
	row.RecordsJSON = string(b)
	row.Version++
	row.Revision++
	row.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindGatewayConfigs(tx, m.names)
	if version == 0 {
		err = q.WithContext(ctx).Create(row)
	} else {
		info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Version.Eq(version)).Select(q.Enabled, q.MachineID, q.DefaultModel, q.RecordsJSON, q.Version, q.Revision, q.UpdatedAt).Updates(row)
		err = e
		if err == nil && info.RowsAffected != 1 {
			err = ErrVersionConflict
		}
	}
	if err != nil {
		return GatewayView{}, err
	}
	prices := query.BindPrices(tx, m.names)
	for _, entry := range in.Models {
		id, e := m.ids.Next()
		if e != nil {
			return GatewayView{}, e
		}
		if id <= 0 {
			return GatewayView{}, ErrUnavailable
		}
		prior, e := prices.WithContext(ctx).Where(prices.TenantID.Eq(tenant), prices.Runtime.Eq(entry.Runtime), prices.Model.Eq(entry.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).Find()
		if e != nil {
			return GatewayView{}, e
		}
		version := 1
		if len(prior) > 0 {
			version = prior[0].Version + 1
		}
		price := &model.ModelPrice{ID: guid.ID(id), TenantID: tenant, Runtime: entry.Runtime, Model: entry.ID, InputMicros: entry.InputMicros, OutputMicros: entry.OutputMicros, CacheReadMicros: entry.CacheReadMicros, CacheWriteMicros: entry.CacheWriteMicros, Version: version, UpdatedAt: row.UpdatedAt}
		if e = prices.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "runtime"}, {Name: "model"}}, DoUpdates: clause.AssignmentColumns([]string{"input_micros", "output_micros", "cache_read_micros", "cache_write_micros", "deleted_at", "version", "updated_at"})}).Create(price); e != nil {
			return GatewayView{}, e
		}
	}
	if err = m.gatewayEventOn(ctx, tx, tenant, row.ID, "gateway.config_changed", int64(row.Version)); err != nil {
		return GatewayView{}, err
	}
	return gatewayView(row)
}

// CreateGatewayKeyOn generates a credential and seals it with deployment-managed
// keyringx. Only its opaque row reference and last four characters leave this API.
func (m *Module) CreateGatewayKeyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) (GatewayKeyView, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return GatewayKeyView{}, err
	}
	return m.storeGatewayKeyOn(ctx, tx, tenant, base64.RawURLEncoding.EncodeToString(raw), false)
}

func validateGatewaySecret(plain string) error {
	if len(plain) < 4 || len(plain) > 4096 || strings.IndexFunc(plain, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func (m *Module) storeGatewayKeyOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, plain string, provider bool) (GatewayKeyView, error) {
	if err := validateGatewaySecret(plain); err != nil {
		return GatewayKeyView{}, err
	}
	if err := fleetTransaction(tx, tenant, tenant); err != nil {
		return GatewayKeyView{}, err
	}
	if m == nil || m.ids == nil {
		return GatewayKeyView{}, ErrUnavailable
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return GatewayKeyView{}, err
	}
	key, e := secretx.Resolve("gateway_encryption_key", m.config.GatewayEncryptionKey, m.config.GatewayEncryptionKeyFile, secretx.DefaultMaxBytes)
	if e != nil {
		return GatewayKeyView{}, ErrUnavailable
	}
	ring, e := keyringx.New("fleet:gateway:", key, m.config.PreviousGatewayEncryptionKeys...)
	if e != nil {
		return GatewayKeyView{}, ErrUnavailable
	}
	id, e := m.ids.Next()
	if e != nil {
		return GatewayKeyView{}, e
	}
	if id <= 0 {
		return GatewayKeyView{}, ErrUnavailable
	}
	sealed, e := ring.Seal("credential", []byte(tenant.String()+":"+guid.ID(id).String()), []byte(plain))
	if e != nil {
		return GatewayKeyView{}, ErrUnavailable
	}
	hashInput := plain
	if provider {
		hashInput = "provider:" + tenant.String() + ":" + guid.ID(id).String() + ":" + plain
	}
	hash := sha256.Sum256([]byte(hashInput))
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.GatewayKey{ID: guid.ID(id), TenantID: tenant, Sealed: sealed, KeyHash: hex.EncodeToString(hash[:]), Last4: plain[len(plain)-4:], CreatedAt: now}
	if e = query.BindGatewayKeys(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return GatewayKeyView{}, e
	}
	if e = m.gatewayEventOn(ctx, tx, tenant, r.ID, "gateway.key_created", 1); e != nil {
		return GatewayKeyView{}, e
	}
	return GatewayKeyView{ID: r.ID, Last4: r.Last4, CreatedAt: now}, nil
}

func (m *Module) openGatewayKey(tenant guid.ID, key *model.GatewayKey) ([]byte, error) {
	secret, err := secretx.Resolve("gateway_encryption_key", m.config.GatewayEncryptionKey, m.config.GatewayEncryptionKeyFile, secretx.DefaultMaxBytes)
	if err != nil {
		return nil, err
	}
	ring, err := keyringx.New("fleet:gateway:", secret, m.config.PreviousGatewayEncryptionKeys...)
	if err != nil {
		return nil, err
	}
	return ring.Open("credential", []byte(tenant.String()+":"+key.ID.String()), key.Sealed)
}

// QueueGatewayRequestOn is metadata-only while the proxy is unavailable. Record
// the real FAILED outcome immediately; no QUEUED success or dispatch is invented.
func (m *Module) QueueGatewayRequestOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, modelID string) (GatewayRequestView, error) {
	return m.queueGatewayRequestOn(ctx, tx, tenant, modelID, nil, 0, 0)
}

// QueueProjectGatewayRequestOn checks the exact project budget before recording
// a proxy attempt. The unavailable proxy consumes no tokens and settles at zero.
func (m *Module) QueueProjectGatewayRequestOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, modelID string, input, output int64) (GatewayRequestView, error) {
	if project <= 0 || input <= 0 || input > 1000000 || output <= 0 || output > 1000000 {
		return GatewayRequestView{}, ErrInvalidArgument
	}
	return m.queueGatewayRequestOn(ctx, tx, tenant, modelID, &project, input, output)
}

func (m *Module) queueGatewayRequestOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, modelID string, project *guid.ID, input, output int64) (GatewayRequestView, error) {
	if err := fleetTransaction(tx, tenant, tenant); err != nil {
		return GatewayRequestView{}, err
	}
	if m == nil || m.ids == nil {
		return GatewayRequestView{}, ErrUnavailable
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "gateway_manage"); err != nil {
		return GatewayRequestView{}, err
	}
	config, e := m.gatewayOn(ctx, tx, tenant, true)
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return GatewayRequestView{}, ErrVersionConflict
	}
	if e != nil {
		return GatewayRequestView{}, e
	}
	v, e := gatewayView(config)
	if e != nil {
		return GatewayRequestView{}, e
	}
	found := false
	var selected GatewayModel
	for _, model := range v.Models {
		found = found || model.ID == modelID
		if model.ID == modelID {
			selected = model
		}
	}
	if !found {
		return GatewayRequestView{}, ErrInvalidArgument
	}
	id, e := m.ids.Next()
	if e != nil {
		return GatewayRequestView{}, e
	}
	if id <= 0 {
		return GatewayRequestView{}, ErrUnavailable
	}
	if project != nil {
		if m.gatewayBudgets.ReserveOn == nil || m.gatewayBudgets.SettleOn == nil {
			return GatewayRequestView{}, ErrUnavailable
		}
		cost, _, err := EstimateTokenCost(input, output, &TokenPrice{InputMicros: selected.InputMicros, OutputMicros: selected.OutputMicros})
		if err != nil {
			return GatewayRequestView{}, err
		}
		var limit *int64
		for _, entry := range v.Budgets {
			if entry.ProjectID == *project {
				limit = &entry.LimitMicros
				break
			}
		}
		if err = m.gatewayBudgets.ReserveOn(ctx, tx, tenant, *project, guid.ID(id), cost, limit); err != nil {
			return GatewayRequestView{}, err
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := &model.GatewayRequest{ID: guid.ID(id), ProjectID: project, TenantID: tenant, Revision: config.Revision, Model: modelID, State: "FAILED", Reason: "RUNNER_GATEWAY_UNAVAILABLE", CreatedAt: now, FinishedAt: &now}
	q := query.BindGatewayRequests(tx, m.names)
	if e = q.WithContext(ctx).Create(r); e != nil {
		return GatewayRequestView{}, e
	}
	if project != nil {
		if e = m.gatewayBudgets.SettleOn(ctx, tx, tenant, *project, r.ID); e != nil {
			return GatewayRequestView{}, e
		}
	}
	old, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.In("COMPLETED", "FAILED", "CANCELLED")).Order(q.ID.Desc()).Offset(100).Find() //nolint:misspell // Frozen database protocol spelling.
	if e != nil {
		return GatewayRequestView{}, e
	}
	for _, entry := range old {
		if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(entry.ID)).Delete(); e != nil {
			return GatewayRequestView{}, e
		}
	}
	if e = m.gatewayEventOn(ctx, tx, tenant, r.ID, "gateway.request_changed", 1); e != nil {
		return GatewayRequestView{}, e
	}
	return gatewayRequestView(r), nil
}
