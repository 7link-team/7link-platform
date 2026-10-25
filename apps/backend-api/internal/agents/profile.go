package agents

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type ProfileReadInput struct{ AgentPath }
type ProfileUpdateInput struct {
	AgentPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           fleet.AgentProfilePatch
}

func (m *Module) injectProfiles(s *module.Services) error {
	if v, e := module.Resolve[identity.AgentAvatar](s); e == nil {
		m.profileAvatar = v
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if v, e := module.Resolve[knowledge.MemoryPolicyValidator](s); e == nil {
		m.memoryPolicy = v
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	value, e := module.Resolve[fleet.AgentProfiles](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	m.profiles = value
	if value.BindProcessObserver != nil {
		return value.BindProcessObserver(m.observeProcessOn)
	}
	return nil
}
func (m *Module) registerProfileHTTP(api huma.API) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: agentPath + path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_profile.read", http.MethodGet, "/profile", "Read an authorized Agent's private profile and runtime configuration"), m.profileHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_profile.update", http.MethodPatch, "/profile", "Edit an Agent profile with current Human authority and version fencing"), m.updateProfileHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_runtime_config.read", http.MethodGet, "/runtime-config", "Read the authenticated Agent's own runtime configuration"), m.runtimeConfigHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_avatar.candidates", http.MethodGet, "/avatar-candidates", "Read six deterministic avatar candidates for an authorized Agent"), m.avatarCandidatesHTTP)
}
func profileError(e error) error {
	switch {
	case errors.Is(e, fleet.ErrInvalidArgument):
		return ErrInvalid
	case errors.Is(e, fleet.ErrVersionConflict):
		return ErrConflict
	case errors.Is(e, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(e, fleet.ErrUnavailable):
		return ErrUnavailable
	default:
		return e
	}
}
func (m *Module) profileOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, self bool) (fleet.AgentProfile, error) {
	permission := "agent_workspace_read"
	if self {
		permission = "SELF"
	}
	if _, e := m.authorize(ctx, tx, tenant, agent, permission, !self); e != nil {
		return fleet.AgentProfile{}, e
	}
	if m.profiles.ReadOn == nil {
		return fleet.AgentProfile{}, ErrUnavailable
	}
	value, e := m.profiles.ReadOn(ctx, tx, tenant, agent)
	if e != nil {
		return value, profileError(e)
	}
	return m.profileWithAvatar(ctx, value)
}
func (m *Module) updateProfileOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in fleet.AgentProfilePatch) (fleet.AgentProfile, error) {
	if _, e := m.authorize(ctx, tx, tenant, agent, "agent_manage", true); e != nil {
		return fleet.AgentProfile{}, e
	}
	if m.profiles.UpdateOn == nil {
		return fleet.AgentProfile{}, ErrUnavailable
	}
	// Memory policy validation is owned by Knowledge; absent validation must
	// never silently accept a policy that retrieval cannot honor.
	if in.MemoryPolicy != nil {
		if m.memoryPolicy.Validate == nil {
			return fleet.AgentProfile{}, ErrUnavailable
		}
		if e := m.memoryPolicy.Validate(ctx, tenant, *in.MemoryPolicy); e != nil {
			return fleet.AgentProfile{}, ErrInvalid
		}
	}
	value, e := m.profiles.UpdateOn(ctx, tx, tenant, agent, in)
	if e != nil {
		return fleet.AgentProfile{}, profileError(e)
	}
	if e = m.record(ctx, tx, tenant, agent, "profile.updated", true); e != nil {
		return fleet.AgentProfile{}, e
	}
	return m.profileWithAvatar(ctx, value)
}

func (m *Module) profileWithAvatar(ctx context.Context, value fleet.AgentProfile) (fleet.AgentProfile, error) {
	if m.profileAvatar.Render == nil {
		return value, nil
	}
	pixels, e := m.profileAvatar.Render(ctx, value.AvatarStyle, value.AvatarSeed)
	if e != nil {
		return fleet.AgentProfile{}, ErrUnavailable
	}
	value.AvatarURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixels)
	return value, nil
}

type AvatarCandidatesInput struct {
	AgentPath
	Style string `query:"style" enum:"ROBOT,PIXEL,MONSTER,GEOMETRIC" default:"PIXEL"`
	Batch int    `query:"batch" minimum:"0" maximum:"1000000" default:"0"`
}
type AvatarCandidate struct {
	Style string `json:"style"`
	Seed  int64  `json:"seed"`
	URL   string `json:"url"`
}

func (m *Module) avatarCandidatesHTTP(ctx context.Context, in *AvatarCandidatesInput) (*ProtocolBody[[]AvatarCandidate], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) ([]AvatarCandidate, error) {
		if _, e := m.authorize(ctx, tx, t, a, "agent_read", true); e != nil {
			return nil, e
		}
		return m.avatarCandidates(ctx, t, a, in.Style, in.Batch)
	})
}

func (m *Module) avatarCandidates(ctx context.Context, tenant, subject guid.ID, style string, batch int) ([]AvatarCandidate, error) {
	if m.profileAvatar.Render == nil {
		return nil, ErrUnavailable
	}
	if batch < 0 || batch > 1000000 {
		return nil, ErrInvalid
	}
	out := make([]AvatarCandidate, 0, 6)
	for i := 0; i < 6; i++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%d", tenant, subject, style, batch, i)))
		seed := int64(binary.BigEndian.Uint64(hash[:8]) & ((1 << 53) - 1))
		pixels, err := m.profileAvatar.Render(ctx, style, seed)
		if err != nil {
			return nil, ErrInvalid
		}
		out = append(out, AvatarCandidate{Style: style, Seed: seed, URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pixels)})
	}
	return out, nil
}
func (m *Module) profileHTTP(ctx context.Context, in *ProfileReadInput) (*ProtocolBody[fleet.AgentProfile], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (fleet.AgentProfile, error) { return m.profileOn(ctx, tx, t, a, false) })
}

type RuntimeSkill struct {
	Key     string `json:"skill_key"`
	Version int    `json:"version_no"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type AgentRuntimeConfig struct {
	fleet.AgentProfile
	Skills []RuntimeSkill `json:"skills"`
}

func (m *Module) runtimeConfigOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (AgentRuntimeConfig, error) {
	p, e := m.profileOn(ctx, tx, tenant, agent, true)
	if e != nil {
		return AgentRuntimeConfig{}, e
	}
	out := AgentRuntimeConfig{AgentProfile: p, Skills: []RuntimeSkill{}}
	catalog, e := m.skillCatalogOn(ctx, tx, tenant, agent)
	if e != nil {
		return out, e
	}
	q := query.BindToolEntry(tx, m.names)
	v := query.BindToolVersion(tx, m.names)
	for _, skill := range catalog.Skills {
		entry, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(skill.ID), q.Kind.Eq("SKILL"), q.State.Eq("ACTIVE")).First()
		if e != nil {
			return out, e
		}
		body, version, hash := entry.SkillBody, entry.Version, entry.ContentHash
		versions, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(skill.ID), v.State.Eq("ACTIVE")).Order(v.VersionNo.Desc()).Limit(1).Find()
		if e != nil {
			return out, e
		}
		if len(versions) > 0 {
			body, version, hash = versions[0].SkillBody, versions[0].VersionNo, versions[0].ContentHash
		}
		sum := sha256.Sum256([]byte(body))
		if hex.EncodeToString(sum[:]) != hash {
			return out, ErrUnavailable
		}
		out.Skills = append(out.Skills, RuntimeSkill{Key: entry.EntryKey, Version: version, Content: body, SHA256: hash})
	}
	return out, nil
}
func (m *Module) runtimeConfigHTTP(ctx context.Context, in *ProfileReadInput) (*ProtocolBody[AgentRuntimeConfig], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (AgentRuntimeConfig, error) { return m.runtimeConfigOn(ctx, tx, t, a) })
}
func (m *Module) updateProfileHTTP(ctx context.Context, in *ProfileUpdateInput) (*CommandOutput[fleet.AgentProfile], error) {
	t, a, e := pathIDs(ctx, in.AgentPath)
	if e != nil {
		return nil, agentHTTPError(ctx, e)
	}
	return agentCommand(ctx, m, in.IdempotencyKey, "agent_profile.update", agentPath+"/profile", agentParams(in.AgentPath), in.Body, func(tx *gorm.DB) error { _, e := m.authorize(ctx, tx, t, a, "agent_manage", true); return e }, func(tx *gorm.DB) (fleet.AgentProfile, error) { return m.updateProfileOn(ctx, tx, t, a, in.Body) })
}
