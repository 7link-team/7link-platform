package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/secretx"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type ToolInput struct {
	PresetKey   string           `json:"preset_key,omitempty" maxLength:"64"`
	FromLocal   *ToolLocalSource `json:"from_local,omitempty"`
	EntryKey    string           `json:"entry_key" minLength:"2" maxLength:"64" pattern:"^[a-z0-9][a-z0-9-]{1,63}$"`
	Kind        string           `json:"kind" enum:"MCP_SERVER,SKILL"`
	Title       string           `json:"title" minLength:"1" maxLength:"200"`
	Transport   string           `json:"transport" enum:",STREAMABLE_HTTP,STDIO_PACKAGE"`
	Endpoint    string           `json:"endpoint" maxLength:"512"`
	ContentHash string           `json:"content_hash" maxLength:"64"`
	SkillBody   string           `json:"skill_body" maxLength:"64000"`
	Credential  string           `json:"credential,omitempty" maxLength:"8000" writeOnly:"true"`
	ReauthProof string           `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
}
type ToolView struct {
	ID            guid.ID              `json:"id"`
	EntryKey      string               `json:"entry_key"`
	Kind          string               `json:"kind"`
	Title         string               `json:"title"`
	Transport     string               `json:"transport"`
	Endpoint      string               `json:"endpoint"`
	ContentHash   string               `json:"content_hash"`
	SkillBody     string               `json:"skill_body,omitempty"`
	State         string               `json:"state"`
	Version       int                  `json:"version"`
	HasCredential bool                 `json:"has_credential"`
	Credential    ToolCredentialStatus `json:"credential"`
	Next          []Next               `json:"next"`
}
type ToolCredentialStatus struct {
	Set                 bool       `json:"set"`
	Last4               string     `json:"last4"`
	RotatedAt           *time.Time `json:"rotated_at,omitempty"`
	UpstreamAuthFailing bool       `json:"upstream_auth_failing"`
}
type GrantView struct {
	ID               guid.ID `json:"id"`
	ToolID           guid.ID `json:"tool_id"`
	AgentPrincipalID guid.ID `json:"agent_principal_id"`
	State            string  `json:"state"`
	Version          int     `json:"version"`
	Next             []Next  `json:"next"`
}

func toolView(r *model.ToolEntry) ToolView {
	return ToolView{ID: r.ID, EntryKey: r.EntryKey, Kind: r.Kind, Title: r.Title, Transport: r.Transport, Endpoint: r.Endpoint, ContentHash: r.ContentHash, SkillBody: r.SkillBody, State: r.State, Version: r.Version, HasCredential: r.CredentialSealed != "", Credential: ToolCredentialStatus{Set: r.CredentialSealed != "", Last4: r.CredentialLast4, RotatedAt: r.CredentialRotatedAt, UpstreamAuthFailing: r.UpstreamAuthFailures >= 5}, Next: []Next{{Operation: "agent_tools.grant"}}}
}
func grantView(r *model.ToolGrant) GrantView {
	return GrantView{ID: r.ID, ToolID: r.ToolID, AgentPrincipalID: r.AgentPrincipalID, State: r.State, Version: r.Version, Next: []Next{{Operation: "agent_tools.list"}}}
}
func (m *Module) keys() (*keyringx.Keyring, error) {
	active, err := secretx.Resolve("agents.encryption_key", m.config.EncryptionKey, m.config.EncryptionKeyFile, 4096)
	if err != nil {
		return nil, ErrUnavailable
	}
	if active == "" {
		return nil, ErrUnavailable
	}
	previous, err := secretx.ResolveList("agents.previous_encryption_keys", m.config.PreviousEncryptionKeys, m.config.PreviousEncryptionKeyFiles, 4096)
	if err != nil {
		return nil, ErrUnavailable
	}
	ring, err := keyringx.New("agents:", active, previous...)
	if err != nil {
		return nil, ErrUnavailable
	}
	return ring, nil
}
func (m *Module) teamToolAuthority(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	// A target agent is not required to manage a team catalog. Authenticate and
	// lock the human first; never let agent-supplied roles grant this authority.
	actor := actorID(ctx)
	if actor <= 0 || tenant != tenantID(ctx) {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil || m.tenantPolicy == nil {
		return ErrUnavailable
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	h, human, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return err
	}
	if !human || h.ID != actor {
		return ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	c, err := m.tenantPolicy(ctx, tx, tenant, "tool_catalog_manage", actor)
	if err != nil {
		return err
	}
	if !c.AllowAll {
		return ErrForbidden
	}
	return nil
}

var entryKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

func (m *Module) createToolOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in ToolInput) (ToolView, error) {
	var out ToolView
	if err := m.teamToolAuthority(ctx, tx, tenant); err != nil {
		return out, err
	}
	source, ref, err := m.prepareToolSourceOn(ctx, tx, tenant, &in)
	if err != nil {
		return out, err
	}
	in, err = validateToolConfiguration(in)
	if err != nil {
		return out, err
	}
	if m.ids == nil {
		return out, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	sealed := ""
	if in.Credential != "" {
		if m.credentialProof == nil || in.ReauthProof == "" {
			return out, ErrForbidden
		}
		if err = m.credentialProof(ctx, tx, tenant, in.EntryKey, in.ReauthProof); err != nil {
			return out, ErrForbidden
		}
		ring, e := m.keys()
		if e != nil {
			return out, e
		}
		sealed, e = ring.Seal("tool_credential", []byte(tenant.String()+":"+guid.ID(id).String()), []byte(in.Credential))
		if e != nil {
			return out, ErrUnavailable
		}
	}
	row := model.ToolEntry{ID: guid.ID(id), TenantID: tenant, EntryKey: in.EntryKey, Kind: in.Kind, Title: strings.TrimSpace(in.Title), Transport: in.Transport, Endpoint: in.Endpoint, ContentHash: in.ContentHash, SkillBody: in.SkillBody, CredentialSealed: sealed, State: "ACTIVE", Version: 1, CreatedAt: m.now()}
	if in.Credential != "" {
		row.CredentialLast4 = credentialLast4(in.Credential)
		now := m.now()
		row.CredentialRotatedAt = &now
	}
	if err = query.BindToolEntry(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return out, err
	}
	if source != "MANUAL" {
		row.State = "DISABLED"
		q := query.BindToolEntry(tx, m.names)
		if _, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.TenantID.Eq(tenant)).Update(q.State, row.State); err != nil {
			return out, err
		}
	}
	if _, err = m.insertToolVersionOn(ctx, tx, &row, 1, source, ref, "Initial publication"); err != nil {
		return out, err
	}
	if err = m.record(ctx, tx, tenant, row.ID, "TOOL_CREATED", true); err != nil {
		return out, err
	}
	return toolView(&row), nil
}
func (m *Module) toolsOn(ctx context.Context, tx *gorm.DB, tenant, agent, after guid.ID, limit int) ([]ToolView, bool, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, false, ErrInvalid
	}
	if agent == 0 {
		if err := m.teamToolAuthority(ctx, tx, tenant); err != nil {
			return nil, false, err
		}
	} else {
		permission := "agent_read"
		if agent == actorID(ctx) {
			permission = "SELF"
		}
		if _, err := m.authorize(ctx, tx, tenant, agent, permission, false); err != nil {
			return nil, false, err
		}
	}
	q := query.BindToolEntry(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after))
	if agent > 0 {
		g := query.BindToolGrant(tx, m.names)
		request = request.Where(q.State.Eq("ACTIVE"), q.WithContext(ctx).Columns(q.ID).In(g.WithContext(ctx).Select(g.ToolID).Where(g.TenantID.Eq(tenant), g.AgentPrincipalID.Eq(agent), g.State.Eq("ACTIVE"))))
	}
	rows, err := request.Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := []ToolView{}
	for _, r := range rows {
		out = append(out, toolView(r))
	}
	return out, more, nil
}
func (m *Module) grantOn(ctx context.Context, tx *gorm.DB, tenant, tool, agent guid.ID, state string) (GrantView, error) {
	if tool <= 0 || agent <= 0 || !slices.Contains([]string{"ACTIVE", "REVOKED"}, state) {
		return GrantView{}, ErrInvalid
	}
	if err := m.teamToolAuthority(ctx, tx, tenant); err != nil {
		return GrantView{}, err
	}
	if m.agents.ByPrincipalOn == nil || m.ids == nil {
		return GrantView{}, ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return GrantView{}, err
	}
	if !found || a.PrincipalID != agent || a.TenantID != tenant || a.State != "ACTIVE" {
		return GrantView{}, ErrNotFound
	}
	tools := query.BindToolEntry(tx, m.names)
	toolRequest := tools.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(tools.TenantID.Eq(tenant), tools.ID.Eq(tool))
	if state == "ACTIVE" {
		toolRequest = toolRequest.Where(tools.State.Eq("ACTIVE"))
	}
	_, err = toolRequest.First()
	if err != nil {
		return GrantView{}, err
	}
	q := query.BindToolGrant(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ToolID.Eq(tool), q.AgentPrincipalID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		id, e := m.ids.Next()
		if e != nil {
			return GrantView{}, e
		}
		if id <= 0 {
			return GrantView{}, ErrUnavailable
		}
		r = &model.ToolGrant{ID: guid.ID(id), TenantID: tenant, ToolID: tool, AgentPrincipalID: agent, State: state, Version: 1, CreatedAt: m.now()}
		err = q.WithContext(ctx).Create(r)
	} else if err == nil {
		if r.State == state {
			return grantView(r), nil
		}
		res, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.TenantID.Eq(tenant), q.Version.Eq(r.Version)).Updates(map[string]any{"state": state, "version": r.Version + 1})
		err = e
		if err == nil && res.RowsAffected != 1 {
			err = ErrConflict
		}
		r.State = state
		r.Version++
	}
	if err != nil {
		return GrantView{}, err
	}
	if err = m.record(ctx, tx, tenant, agent, "TOOL_GRANT_CHANGED", true); err != nil {
		return GrantView{}, err
	}
	return grantView(r), nil
}

// CredentialProofConnector binds the existing IAM purpose-bound step-up consumer.
// The target includes the tenant and unique catalog entry key.
type CredentialProofConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, string, string) error) error
}

func (m *Module) bindCredentialProof(fn func(context.Context, *gorm.DB, guid.ID, string, string) error) error {
	if fn == nil || m.credentialProof != nil {
		return ErrInvalid
	}
	m.credentialProof = fn
	return nil
}

func validateToolConfiguration(in ToolInput) (ToolInput, error) {
	if !entryKeyPattern.MatchString(in.EntryKey) || !slices.Contains([]string{"MCP_SERVER", "SKILL"}, in.Kind) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || len(in.Endpoint) > 512 || len(in.SkillBody) > 64000 || len(in.Credential) > 8000 {
		return in, ErrInvalid
	}
	if in.Kind == "MCP_SERVER" {
		if in.SkillBody != "" || !slices.Contains([]string{"STREAMABLE_HTTP", "STDIO_PACKAGE"}, in.Transport) {
			return in, ErrInvalid
		}
		u, err := url.Parse(in.Endpoint)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return in, ErrInvalid
		}
		if in.Transport == "STREAMABLE_HTTP" && !validWakeURL(in.Endpoint) {
			return in, ErrInvalid
		}
		if in.Transport == "STDIO_PACKAGE" {
			if in.Credential != "" || len(in.ContentHash) != 64 || !strings.HasPrefix(in.Endpoint, "npm:") {
				return in, ErrInvalid
			}
			if _, err = hex.DecodeString(in.ContentHash); err != nil {
				return in, ErrInvalid
			}
		}
	} else {
		if in.SkillBody == "" || in.Transport != "" || in.Endpoint != "" || in.Credential != "" {
			return in, ErrInvalid
		}
		hash := sha256.Sum256([]byte(in.SkillBody))
		in.ContentHash = hex.EncodeToString(hash[:])
	}
	if in.Kind == "MCP_SERVER" && in.Transport == "STREAMABLE_HTTP" {
		in.ContentHash = toolConfigurationHash(in.Transport, in.Endpoint, "")
	}
	return in, nil
}

// These configurations contain strings only, so sorted JSON keys and unescaped
// UTF-8 produce the JCS representation without numeric normalization.
func toolConfigurationHash(transport, endpoint, integrity string) string {
	config := map[string]string{"url": endpoint}
	if transport == "STDIO_PACKAGE" {
		config = map[string]string{"package": endpoint, "sha256": integrity}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(config) != nil {
		return ""
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buffer.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}
