package templates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrProviderAdapter            = errors.New("templates: provider adapter unavailable or unknown")
	ErrProviderLicense            = errors.New("templates: provider licence notice requires Human acceptance") //nolint:misspell // Preserve the published domain or serialized contract spelling.
	ErrProviderBinding            = errors.New("templates: provider kind or current health does not permit binding")
	ErrProviderSecretsUnavailable = fmt.Errorf("%w: provider credential sealing requires the secrets module", ErrUnavailable)
	providerKeyPattern            = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	providerEnvPattern            = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	reportDigestPatternProvider   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const providerSecretPurpose = "templates.provider_credential"

var providerKinds = []string{"VIDEO_GEN", "IMAGE_GEN", "TTS", "MUSIC_GEN", "ASR_ALIGN", "VLM_JUDGE", "LLM_JUDGE"}

type ProviderAdapterView struct {
	Key                string   `json:"key"`
	Kind               string   `json:"kind"`
	LicenseNotice      string   `json:"license_notice"`
	RegionNotice       string   `json:"region_notice"`
	PublicConfigFields []string `json:"public_config_fields"`
	SecretRequired     bool     `json:"secret_required"`
}

// Connection exists only while invoking the trusted adapter. The environment
// contains at most the one credential key declared by that adapter.
type ProviderConnection struct {
	Config      json.RawMessage
	MachineID   guid.ID
	Environment map[string]string
}
type ProviderHealth struct {
	Available   bool
	Limitations string
}
type ProviderOutput struct {
	FileRef      string `json:"file_ref"`
	SHA256       string `json:"sha256"`
	Model        string `json:"model"`
	ModelVersion string `json:"model_version"`
	Seed         string `json:"seed,omitempty"`
}
type ProviderPoll struct {
	State        string           `json:"state"`
	Outputs      []ProviderOutput `json:"outputs"`
	ReportedCost string           `json:"reported_cost,omitempty"`
	Currency     string           `json:"currency,omitempty"`
}
type ProviderAdapter struct {
	Definition           ProviderAdapterView
	SecretEnvironmentKey string
	ValidateConfig       func(json.RawMessage) error
	Health               func(context.Context, ProviderConnection) (ProviderHealth, error)
	Generate             func(context.Context, ProviderConnection, string, json.RawMessage) (string, error)
	Poll                 func(context.Context, ProviderConnection, string) (ProviderPoll, error)
	Cancel               func(context.Context, ProviderConnection, string) error
}
type ProviderAdapterConnector struct{ Register func(ProviderAdapter) error }

// This source-owned callback must prove the real execution and its current
// authority. A positive caller-supplied execution ID alone is never proof.
type ProviderExecutionAuthority func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error
type ProviderExecutionConnector struct {
	Bind func(ProviderExecutionAuthority) error
}

func (m *Module) provideProviders(s *module.Services) error {
	if e := m.provideRubricScores(s); e != nil {
		return e
	}
	if err := m.provideVideo(s); err != nil {
		return err
	}
	if m.providerAdapters == nil {
		m.providerAdapters = map[string]ProviderAdapter{}
	}
	if _, exists := m.providerAdapters["verifier_http_v1"]; !exists {
		m.providerAdapters["verifier_http_v1"] = verifierHTTPAdapter()
	}
	for _, kind := range providerKinds {
		key := strings.ToLower(kind) + "_http_v1"
		if _, exists := m.providerAdapters[key]; !exists {
			m.providerAdapters[key] = providerHTTPAdapter(key, kind, "provider-v1")
		}
	}
	if err := module.Provide[ProviderAdapterConnector](s, ProviderAdapterConnector{Register: func(a ProviderAdapter) error {
		d := a.Definition
		if m.calibrationSealed || !providerKeyPattern.MatchString(d.Key) || !slices.Contains(providerKinds, d.Kind) || strings.TrimSpace(d.LicenseNotice) == "" || len(d.LicenseNotice) > 4000 || len(d.RegionNotice) > 2000 || len(d.PublicConfigFields) > 32 || a.ValidateConfig == nil || a.Health == nil || a.Generate == nil || a.Poll == nil || a.Cancel == nil || a.SecretEnvironmentKey != "" && !providerEnvPattern.MatchString(a.SecretEnvironmentKey) || d.SecretRequired && a.SecretEnvironmentKey == "" {
			return ErrInvalid
		}
		if _, exists := m.providerAdapters[d.Key]; exists {
			return ErrConflict
		}
		seen := map[string]bool{}
		for _, key := range d.PublicConfigFields {
			if !providerKeyPattern.MatchString(key) || seen[key] || providerSensitiveKey(key) {
				return ErrInvalid
			}
			seen[key] = true
		}
		a.Definition.PublicConfigFields = slices.Clone(d.PublicConfigFields)
		m.providerAdapters[d.Key] = a
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide[ProviderExecutionConnector](s, ProviderExecutionConnector{Bind: func(f ProviderExecutionAuthority) error {
		if f == nil || m.providerExecution != nil || m.calibrationSealed {
			return ErrInvalid
		}
		m.providerExecution = f
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[ProviderGateway](s, ProviderGateway{RequestOn: m.requestProviderGenerationOn, ReadOn: m.readProviderGenerationOn, CancelOn: m.cancelProviderGenerationOn})
}
func (m *Module) injectProviders(s *module.Services) error {
	var err error
	m.providerSealer, err = module.Resolve[secrets.Sealer](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil // Credential operations explicitly reject an unavailable sealer.
	}
	if err != nil {
		return err
	}
	return m.providerSealer.RegisterPurpose(providerSecretPurpose)
}

type ProviderDraft struct {
	Kind                string                     `json:"kind" enum:"VIDEO_GEN,IMAGE_GEN,TTS,MUSIC_GEN,ASR_ALIGN,VLM_JUDGE,LLM_JUDGE"`
	AdapterKey          string                     `json:"adapter_key" minLength:"1" maxLength:"64"`
	DisplayName         string                     `json:"display_name" minLength:"1" maxLength:"120"`
	Config              map[string]json.RawMessage `json:"config"`
	Secret              string                     `json:"secret,omitempty" maxLength:"8000" writeOnly:"true"`
	MachineID           guid.ID                    `json:"machine_id"`
	AcceptLicenseNotice bool                       `json:"accept_license_notice"`
}
type ProviderPatch struct {
	DisplayName         *string                     `json:"display_name,omitempty" maxLength:"120"`
	Config              *map[string]json.RawMessage `json:"config,omitempty"`
	Secret              *string                     `json:"secret,omitempty" maxLength:"8000" writeOnly:"true"`
	State               *string                     `json:"state,omitempty" enum:"ENABLED,DISABLED"`
	AcceptLicenseNotice bool                        `json:"accept_license_notice"`
}
type ProviderView struct {
	ID                guid.ID         `json:"id"`
	Kind              string          `json:"kind"`
	AdapterKey        string          `json:"adapter_key"`
	DisplayName       string          `json:"display_name"`
	Config            json.RawMessage `json:"config"`
	HasSecret         bool            `json:"has_secret"`
	MachineID         guid.ID         `json:"machine_id"`
	State             string          `json:"state" enum:"ENABLED,DISABLED,UNAVAILABLE"`
	HealthCheckedAt   *time.Time      `json:"health_checked_at,omitempty"`
	Limitations       string          `json:"limitations"`
	LicenseNotice     string          `json:"license_notice"`
	RegionNotice      string          `json:"region_notice"`
	LicenseAcceptedBy guid.ID         `json:"license_accepted_by"`
	LicenseAcceptedAt time.Time       `json:"license_accepted_at"`
	Version           int             `json:"version"`
}

func providerSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "credential") || strings.Contains(key, "api_key") || strings.Contains(key, "apikey") || strings.Contains(key, "authorization")
}
func providerConfig(a ProviderAdapter, in map[string]json.RawMessage) (string, error) {
	if in == nil {
		in = map[string]json.RawMessage{}
	}
	for key, value := range in {
		if !slices.Contains(a.Definition.PublicConfigFields, key) || providerSensitiveKey(key) || len(value) > 16000 || !json.Valid(value) {
			return "", ErrInvalid
		}
		var decoded any
		if json.Unmarshal(value, &decoded) != nil || providerHasSensitiveFields(decoded) {
			return "", ErrInvalid
		}
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 32000 {
		return "", ErrInvalid
	}
	if err = a.ValidateConfig(raw); err != nil {
		return "", ErrInvalid
	}
	return string(raw), nil
}
func providerHasSensitiveFields(v any) bool {
	switch value := v.(type) {
	case map[string]any:
		for key, item := range value {
			if providerSensitiveKey(key) || providerHasSensitiveFields(item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if providerHasSensitiveFields(item) {
				return true
			}
		}
	}
	return false
}
func providerLicenseHash(a ProviderAdapter) string {
	data, _ := json.Marshal([]string{a.Definition.LicenseNotice, a.Definition.RegionNotice})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (m *Module) providerView(row *model.TemplateProvider) ProviderView {
	a, exists := m.providerAdapters[row.AdapterKey]
	state, note := row.State, row.HealthNote
	if !exists || providerLicenseHash(a) != row.LicenseSHA256 {
		state = "UNAVAILABLE"
		note = "Registered adapter or accepted licence notice is unavailable" //nolint:misspell // Preserve the published domain or serialized contract spelling.
	}
	return ProviderView{row.ID, row.Kind, row.AdapterKey, row.DisplayName, json.RawMessage(row.Config), row.SealedSecret != "", row.MachineID, state, row.HealthCheckedAt, note, a.Definition.LicenseNotice, a.Definition.RegionNotice, row.LicenseAcceptedBy, row.LicenseAcceptedAt, row.Version}
}
func (m *Module) providerOn(ctx context.Context, tx *gorm.DB, id guid.ID, permission string) (*model.TemplateProvider, error) {
	if err := m.authorizeOn(ctx, tx, permission, 0); err != nil {
		return nil, err
	}
	q := query.BindProviders(tx, m.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = m.authorizeOn(ctx, tx, permission, row.CreatedBy); err != nil {
		return nil, err
	}
	return row, nil
}
func (m *Module) createProviderOn(ctx context.Context, tx *gorm.DB, in ProviderDraft) (ProviderView, error) {
	if err := m.authorizeOn(ctx, tx, "integration_manage", authn.PrincipalIDFromContext(ctx)); err != nil {
		return ProviderView{}, err
	}
	a, exists := m.providerAdapters[in.AdapterKey]
	if !exists {
		return ProviderView{}, ErrProviderAdapter
	}
	if a.Definition.Kind != in.Kind || strings.TrimSpace(in.DisplayName) == "" || len(in.DisplayName) > 120 || in.MachineID < 0 || len(in.Secret) > 8000 {
		return ProviderView{}, ErrInvalid
	}
	if !in.AcceptLicenseNotice {
		return ProviderView{}, ErrProviderLicense
	}
	if a.Definition.SecretRequired && in.Secret == "" {
		return ProviderView{}, ErrInvalid
	}
	config, err := providerConfig(a, in.Config)
	if err != nil {
		return ProviderView{}, err
	}
	if m.ids == nil {
		return ProviderView{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return ProviderView{}, err
	}
	now := time.Now().UTC()
	row := &model.TemplateProvider{TenantID: authn.TenantIDFromContext(ctx), ID: guid.ID(id), Kind: in.Kind, AdapterKey: in.AdapterKey, DisplayName: strings.TrimSpace(in.DisplayName), Config: config, MachineID: in.MachineID, State: "UNAVAILABLE", LicenseSHA256: providerLicenseHash(a), LicenseAcceptedBy: authn.PrincipalIDFromContext(ctx), LicenseAcceptedAt: now, CreatedBy: authn.PrincipalIDFromContext(ctx), Version: 1, HealthNote: "No actual health check has completed"}
	if in.Secret != "" {
		row.SealedSecret, err = m.sealProviderSecret(row.TenantID, row.ID, in.Secret)
		if err != nil {
			return ProviderView{}, err
		}
	}
	if err = query.BindProviders(tx, m.names).WithContext(ctx).Create(row); err != nil {
		return ProviderView{}, err
	}
	if err = m.providerEventOn(ctx, tx, row.TenantID, row.ID, "template.provider_created", map[string]any{"kind": row.Kind, "adapter_key": row.AdapterKey, "version": row.Version, "license_sha256": row.LicenseSHA256}); err != nil {
		return ProviderView{}, err
	}
	return m.providerView(row), nil
}
func (m *Module) sealProviderSecret(tenant, id guid.ID, value string) (string, error) {
	if m.providerSealer == nil {
		return "", ErrProviderSecretsUnavailable
	}
	aad, err := secrets.AssociatedData(tenant, id)
	if err != nil {
		return "", err
	}
	plain := []byte(value)
	defer clear(plain)
	return m.providerSealer.Seal(providerSecretPurpose, aad, plain)
}
func (m *Module) patchProviderOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, in ProviderPatch) (ProviderView, error) {
	row, err := m.providerOn(ctx, tx, id, "integration_manage")
	if err != nil {
		return ProviderView{}, err
	}
	if row.Version != version {
		return ProviderView{}, ErrConflict
	}
	a, exists := m.providerAdapters[row.AdapterKey]
	if !exists {
		return ProviderView{}, ErrProviderAdapter
	}
	if in.DisplayName != nil {
		if strings.TrimSpace(*in.DisplayName) == "" || len(*in.DisplayName) > 120 {
			return ProviderView{}, ErrInvalid
		}
		row.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	if in.Config != nil {
		row.Config, err = providerConfig(a, *in.Config)
		if err != nil {
			return ProviderView{}, err
		}
	}
	if in.Secret != nil {
		if len(*in.Secret) > 8000 || a.Definition.SecretRequired && *in.Secret == "" {
			return ProviderView{}, ErrInvalid
		}
		row.SealedSecret = ""
		if *in.Secret != "" {
			row.SealedSecret, err = m.sealProviderSecret(row.TenantID, row.ID, *in.Secret)
			if err != nil {
				return ProviderView{}, err
			}
		}
	}
	if providerLicenseHash(a) != row.LicenseSHA256 {
		if !in.AcceptLicenseNotice {
			return ProviderView{}, ErrProviderLicense
		}
		row.LicenseSHA256 = providerLicenseHash(a)
		row.LicenseAcceptedBy = authn.PrincipalIDFromContext(ctx)
		row.LicenseAcceptedAt = time.Now().UTC()
	}
	if in.State != nil && !slices.Contains([]string{"ENABLED", "DISABLED"}, *in.State) {
		return ProviderView{}, ErrInvalid
	}
	if in.Config != nil || in.Secret != nil || in.State != nil && *in.State == "ENABLED" {
		row.State = "UNAVAILABLE"
		row.HealthCheckedAt = nil
		row.HealthNote = "Configuration changed; an actual health check is required"
	}
	if in.State != nil && *in.State == "DISABLED" {
		row.State = "DISABLED"
	}
	row.Version++
	result := tx.WithContext(ctx).Table(m.names.Providers.String()).Where("tenant_id=? AND id=? AND version=?", row.TenantID, row.ID, version).Updates(map[string]any{"display_name": row.DisplayName, "config": row.Config, "sealed_secret": row.SealedSecret, "state": row.State, "health_checked_at": row.HealthCheckedAt, "health_note": row.HealthNote, "license_sha256": row.LicenseSHA256, "license_accepted_by": row.LicenseAcceptedBy, "license_accepted_at": row.LicenseAcceptedAt, "version": row.Version})
	if result.Error != nil {
		return ProviderView{}, result.Error
	}
	if result.RowsAffected != 1 {
		return ProviderView{}, ErrConflict
	}
	if err = m.providerEventOn(ctx, tx, row.TenantID, row.ID, "template.provider_changed", map[string]any{"state": row.State, "version": row.Version, "license_sha256": row.LicenseSHA256}); err != nil {
		return ProviderView{}, err
	}
	return m.providerView(row), nil
}
func (m *Module) providerConnection(row *model.TemplateProvider) (ProviderConnection, func(), error) {
	a, exists := m.providerAdapters[row.AdapterKey]
	if !exists {
		return ProviderConnection{}, func() {}, ErrProviderAdapter
	}
	c := ProviderConnection{Config: json.RawMessage(row.Config), MachineID: row.MachineID, Environment: map[string]string{}}
	if row.SealedSecret == "" {
		if a.Definition.SecretRequired {
			return c, func() {}, ErrUnavailable
		}
		return c, func() {}, nil
	}
	if m.providerSealer == nil {
		return c, func() {}, ErrProviderSecretsUnavailable
	}
	if a.SecretEnvironmentKey == "" {
		return c, func() {}, ErrUnavailable
	}
	aad, err := secrets.AssociatedData(row.TenantID, row.ID)
	if err != nil {
		return c, func() {}, err
	}
	plain, err := m.providerSealer.Open(providerSecretPurpose, aad, row.SealedSecret)
	if err != nil {
		return c, func() {}, ErrUnavailable
	}
	c.Environment[a.SecretEnvironmentKey] = string(plain)
	clear(plain)
	return c, func() { clear(c.Environment) }, nil
}
func (m *Module) providerEventOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, name string, data map[string]any) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	subject, authority := "template_provider", "IAM:integration_manage"
	actor := events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx)}
	if name == "template.provider_binding_changed" {
		subject, authority = "template_provider_binding", "IAM:project_manage"
	} else if strings.HasPrefix(name, "template.provider_generation_") {
		subject, authority = "template_provider_generation", "EXECUTION:provider_authority"
		if actor.ID <= 0 {
			actor.Kind = "SYSTEM"
		}
	}
	actor.AuthoritySource = authority
	a := audit.NewDomainEvent(ctx, tenant, name, subject, id)
	a.Detail["authority_source"] = authority
	for key, value := range data {
		a.Detail[key] = value
	}
	if err := m.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: name, Actor: actor, Subject: events.Subject{Kind: subject, ID: id}, SubjectVersion: 1, Data: data})
	return err
}
