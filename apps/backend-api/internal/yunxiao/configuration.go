package yunxiao

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrUnavailable = errors.New("yunxiao: unavailable")
	ErrInvalid     = errors.New("yunxiao: invalid configuration")
	ErrForbidden   = errors.New("yunxiao: forbidden")
	ErrNotFound    = errors.New("yunxiao: record not found")
	ErrConflict    = errors.New("yunxiao: version conflict")
)

// ConnectionInput credentials are write-only; an empty token on update preserves it.
type ConnectionInput struct {
	Name           string `json:"name" minLength:"1" maxLength:"80"`
	OrganizationID string `json:"organization_id" minLength:"1" maxLength:"64"`
	APIEndpoint    string `json:"api_endpoint,omitempty" maxLength:"200"`
	Token          string `json:"token,omitempty" maxLength:"8000" writeOnly:"true"`
	WebhookSecret  string `json:"webhook_secret,omitempty" maxLength:"8000" writeOnly:"true"`
	Disabled       bool   `json:"disabled"`
	ReauthProof    string `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
}
type ConnectionView struct {
	ID             guid.ID  `json:"id"`
	Provider       string   `json:"provider"`
	Name           string   `json:"name"`
	OrganizationID string   `json:"organization_id"`
	APIEndpoint    string   `json:"api_endpoint"`
	TokenLast4     string   `json:"token_last4"`
	State          string   `json:"state"`
	TokenScopes    []string `json:"token_scopes"`
	Version        int      `json:"version"`
	Availability   string   `json:"availability"`
}

func connectionView(r *model.Connection) ConnectionView {
	scopes := []string{}
	_ = json.Unmarshal([]byte(r.TokenScopes), &scopes)
	return ConnectionView{ID: r.ID, Provider: r.Provider, Name: r.Name, OrganizationID: r.OrganizationID, APIEndpoint: r.APIEndpoint, TokenLast4: r.TokenLast4, State: r.State, TokenScopes: scopes, Version: r.Version, Availability: "UNAVAILABLE"}
}
func textValid(s string, maximum int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maximum && !strings.ContainsRune(s, 0)
}
func validateConnection(in ConnectionInput) error {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.OrganizationID) == "" || !textValid(in.Name, 80) || !textValid(in.OrganizationID, 64) || !textValid(in.APIEndpoint, 200) || !textValid(in.Token, 8000) || !textValid(in.WebhookSecret, 8000) {
		return ErrInvalid
	}
	if in.APIEndpoint != "" {
		u, e := url.Parse(in.APIEndpoint)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrInvalid
		}
	}
	return nil
}
func (m *Module) keys() (*keyringx.Keyring, error) {
	active, e := secretx.Resolve("yunxiao.encryption_key", m.config.EncryptionKey, m.config.EncryptionKeyFile, 4096)
	if e != nil || active == "" {
		return nil, ErrUnavailable
	}
	previous, e := secretx.ResolveList("yunxiao.previous_encryption_keys", m.config.PreviousEncryptionKeys, m.config.PreviousEncryptionKeyFiles, 4096)
	if e != nil {
		return nil, ErrUnavailable
	}
	ring, e := keyringx.New("integration:", active, previous...)
	if e != nil {
		return nil, ErrUnavailable
	}
	return ring, nil
}
func (m *Module) authorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	return m.tenantAuthorityOn(ctx, tx, tenant, "integration_manage")
}

func (m *Module) tenantAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.policy == nil {
		return ErrUnavailable
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	h, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || h.ID != actor {
		return ErrForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return e
	}
	if !active {
		return ErrForbidden
	}
	c, e := m.policy(ctx, tx, tenant, permission, actor)
	if e != nil {
		return e
	}
	if !c.AllowAll {
		return ErrForbidden
	}
	return nil
}
func (m *Module) changedOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, version int, action string) error {
	if m == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	e := audit.NewDomainEvent(ctx, tenant, action, "integration", id)
	if err := m.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "integration.configuration_changed", Actor: events.Actor{Kind: "HUMAN", ID: authn.ActorIDFromContext(ctx)}, Subject: events.Subject{Kind: "integration", ID: id}, SubjectVersion: int64(version), Data: map[string]any{"action": action}})
	return err
}
func (m *Module) nextID() (guid.ID, error) {
	if m == nil || m.ids == nil {
		return 0, ErrUnavailable
	}
	n, e := m.ids.Next()
	if e != nil {
		return 0, e
	}
	if n <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(n), nil
}
func (m *Module) connectionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.Connection, error) {
	q := query.BindConnections(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		e = ErrNotFound
	}
	return r, e
}
func (m *Module) saveConnectionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, in ConnectionInput, cookie string) (ConnectionView, error) {
	var out ConnectionView
	if e := validateConnection(in); e != nil {
		return out, e
	}
	if e := m.authorityOn(ctx, tx, tenant); e != nil {
		return out, e
	}
	if m.proof == nil || m.now == nil {
		return out, ErrUnavailable
	}
	target := "new"
	if id > 0 {
		target = id.String()
	}
	if e := m.proof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, in.ReauthProof, "integration_credentials", "tenant:"+tenant.String()+":integration:"+target); e != nil {
		return out, e
	}
	now := m.now()
	actor := authn.PrincipalIDFromContext(ctx)
	r := &model.Connection{TenantID: tenant, Provider: "YUNXIAO", Version: 1, CreatedAt: now, CreatedBy: actor}
	if id > 0 {
		var e error
		r, e = m.connectionOn(ctx, tx, tenant, id)
		if e != nil {
			return out, e
		}
		if version <= 0 || r.Version != version {
			return out, ErrConflict
		}
		r.Version++
	} else {
		var e error
		r.ID, e = m.nextID()
		if e != nil {
			return out, e
		}
	}
	r.Name = strings.TrimSpace(in.Name)
	r.OrganizationID = strings.TrimSpace(in.OrganizationID)
	r.APIEndpoint = in.APIEndpoint
	r.UpdatedAt = now
	r.UpdatedBy = actor
	// Configuration never proves connectivity. Changing credentials/endpoints resets the check.
	r.State = "PENDING_CHECK"
	if in.Disabled {
		r.State = "DISABLED"
	}
	r.LastCheckedAt = nil
	r.LastError = nil
	r.TokenScopes = "[]"
	if in.Token != "" || in.WebhookSecret != "" {
		ring, e := m.keys()
		if e != nil {
			return out, e
		}
		aad := []byte(tenant.String() + ":" + r.ID.String())
		if in.Token != "" {
			r.TokenSealed, e = ring.Seal("token", aad, []byte(in.Token))
			if e != nil {
				return out, ErrUnavailable
			}
			chars := []rune(in.Token)
			r.TokenLast4 = "****"
			if len(chars) > 4 {
				r.TokenLast4 = string(chars[len(chars)-4:])
			}
		}
		if in.WebhookSecret != "" {
			r.WebhookSecretSealed, e = ring.Seal("webhook", aad, []byte(in.WebhookSecret))
			if e != nil {
				return out, ErrUnavailable
			}
		}
	}
	q := query.BindConnections(tx, m.names)
	if id == 0 {
		if e := q.WithContext(ctx).Create(r); e != nil {
			return out, e
		}
	} else {
		info, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.Version.Eq(version)).Select(q.Name, q.OrganizationID, q.APIEndpoint, q.TokenSealed, q.TokenLast4, q.WebhookSecretSealed, q.State, q.LastCheckedAt, q.LastError, q.TokenScopes, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(r)
		if e != nil {
			return out, e
		}
		if info.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	if e := m.changedOn(ctx, tx, tenant, 0, r.ID, r.Version, "integration.credentials_changed"); e != nil {
		return out, e
	}
	return connectionView(r), nil
}

type MappingInput struct {
	ExternalProjectID   string            `json:"external_project_id" minLength:"1" maxLength:"64"`
	ExternalProjectName string            `json:"external_project_name" minLength:"1" maxLength:"200"`
	ProjectID           guid.ID           `json:"project_id"`
	SyncMode            string            `json:"sync_mode" enum:"IMPORT_ONLY,READ_MIRROR,BIDIRECTIONAL"`
	FieldMasters        map[string]string `json:"field_masters"`
	CodeupRepositories  []string          `json:"codeup_repositories" maxItems:"100"`
	TrustedPipelines    []string          `json:"trusted_pipelines" maxItems:"100"`
	Paused              bool              `json:"paused"`
}
type MappingView struct {
	ID           guid.ID `json:"id"`
	ConnectionID guid.ID `json:"connection_id"`
	MappingInput
	Version int `json:"version"`
}

func mappingView(r *model.ProjectMapping) MappingView {
	v := MappingView{ID: r.ID, ConnectionID: r.ConnectionID, Version: r.Version, MappingInput: MappingInput{ExternalProjectID: r.ExternalProjectID, ExternalProjectName: r.ExternalProjectName, ProjectID: r.ProjectID, SyncMode: r.SyncMode, Paused: r.Paused, FieldMasters: map[string]string{}, CodeupRepositories: []string{}, TrustedPipelines: []string{}}}
	_ = json.Unmarshal([]byte(r.FieldMasters), &v.FieldMasters)
	_ = json.Unmarshal([]byte(r.CodeupRepositories), &v.CodeupRepositories)
	_ = json.Unmarshal([]byte(r.TrustedPipelines), &v.TrustedPipelines)
	return v
}

var syncFields = []string{"state", "assignee", "title", "description", "iteration", "version", "severity", "priority", "worklog"}

func validateMapping(in MappingInput) error {
	if in.ProjectID <= 0 || strings.TrimSpace(in.ExternalProjectID) == "" || strings.TrimSpace(in.ExternalProjectName) == "" || !textValid(in.ExternalProjectID, 64) || !textValid(in.ExternalProjectName, 200) || !slices.Contains([]string{"IMPORT_ONLY", "READ_MIRROR", "BIDIRECTIONAL"}, in.SyncMode) || len(in.FieldMasters) > len(syncFields) || len(in.CodeupRepositories) > 100 || len(in.TrustedPipelines) > 100 {
		return ErrInvalid
	}
	for k, v := range in.FieldMasters {
		if !slices.Contains(syncFields, k) || !slices.Contains([]string{"MYRMIDON", "YUNXIAO"}, v) {
			return ErrInvalid
		}
	}
	for _, list := range [][]string{in.CodeupRepositories, in.TrustedPipelines} {
		seen := map[string]bool{}
		for _, v := range list {
			if strings.TrimSpace(v) == "" || !textValid(v, 200) || seen[v] {
				return ErrInvalid
			}
			seen[v] = true
		}
	}
	return nil
}
func (m *Module) mappingOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.ProjectMapping, error) {
	q := query.BindProjectMappings(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		e = ErrNotFound
	}
	return r, e
}
func (m *Module) saveMappingOn(ctx context.Context, tx *gorm.DB, tenant, connection, id guid.ID, version int, in MappingInput) (MappingView, error) {
	var out MappingView
	if e := validateMapping(in); e != nil {
		return out, e
	}
	if e := m.authorityOn(ctx, tx, tenant); e != nil {
		return out, e
	}
	if m.projects.AuthorizeOn == nil || m.now == nil {
		return out, ErrUnavailable
	}
	if e := m.projects.AuthorizeOn(ctx, tx, tenant, in.ProjectID, "project_manage"); e != nil {
		return out, e
	}
	r := &model.ProjectMapping{TenantID: tenant, ConnectionID: connection, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now(), Version: 1}
	if id > 0 {
		var e error
		r, e = m.mappingOn(ctx, tx, tenant, id)
		if e != nil {
			return out, e
		}
		if r.ProjectID != in.ProjectID || r.ConnectionID != connection {
			return out, ErrInvalid
		}
		if version <= 0 || r.Version != version {
			return out, ErrConflict
		}
		r.Version++
	} else {
		var e error
		r.ID, e = m.nextID()
		if e != nil {
			return out, e
		}
	}
	if _, e := m.connectionOn(ctx, tx, tenant, connection); e != nil {
		return out, e
	}
	// A trusted pipeline cannot be configured before its actual read scope is verified.
	if len(in.TrustedPipelines) > 0 {
		return out, ErrUnavailable
	}
	r.ProjectID = in.ProjectID
	r.ExternalProjectID = in.ExternalProjectID
	r.ExternalProjectName = in.ExternalProjectName
	r.SyncMode = in.SyncMode
	r.Paused = in.Paused
	r.UpdatedAt = m.now()
	r.UpdatedBy = authn.PrincipalIDFromContext(ctx)
	if in.FieldMasters == nil {
		in.FieldMasters = map[string]string{}
	}
	if in.CodeupRepositories == nil {
		in.CodeupRepositories = []string{}
	}
	if in.TrustedPipelines == nil {
		in.TrustedPipelines = []string{}
	}
	encode := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	r.FieldMasters = encode(in.FieldMasters)
	r.CodeupRepositories = encode(in.CodeupRepositories)
	r.TrustedPipelines = encode(in.TrustedPipelines)
	q := query.BindProjectMappings(tx, m.names)
	if id == 0 {
		if e := q.WithContext(ctx).Create(r); e != nil {
			return out, e
		}
	} else {
		info, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Select(q.ExternalProjectID, q.ExternalProjectName, q.SyncMode, q.FieldMasters, q.CodeupRepositories, q.TrustedPipelines, q.Paused, q.UpdatedAt, q.UpdatedBy, q.Version).Updates(r)
		if e != nil {
			return out, e
		}
		if info.RowsAffected != 1 {
			return out, ErrConflict
		}
	}
	if e := m.changedOn(ctx, tx, tenant, r.ProjectID, r.ID, r.Version, "integration.mapping_changed"); e != nil {
		return out, e
	}
	return mappingView(r), nil
}

// SyncOutcome applies the spec's single-master field rule without remote IO.
// An outcome is a decision only; APPLIED is reserved for actual successful writes.
func SyncOutcome(masters map[string]string, field, direction string, localChanged, externalChanged bool) (string, error) {
	if !slices.Contains(syncFields, field) || !slices.Contains([]string{"IN", "OUT"}, direction) {
		return "", ErrInvalid
	}
	master := masters[field]
	if master == "" {
		master = "MYRMIDON"
	}
	if !slices.Contains([]string{"MYRMIDON", "YUNXIAO"}, master) {
		return "", ErrInvalid
	}
	if direction == "IN" && master != "YUNXIAO" || direction == "OUT" && master != "MYRMIDON" {
		return "SKIPPED_NOT_MASTER", nil
	}
	if localChanged && externalChanged {
		return "CONFLICT_MASTER_WINS", nil
	}
	return "ELIGIBLE", nil
}
