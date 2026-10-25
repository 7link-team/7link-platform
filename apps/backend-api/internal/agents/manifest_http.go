package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

type ManifestReadInput struct {
	TeamPath
	Version     string `query:"version" maxLength:"32" pattern:"^[0-9]+[.][0-9]+[.][0-9]+$"`
	IfNoneMatch string `header:"If-None-Match"`
}
type ManifestReadOutput struct {
	Status int
	ETag   string `header:"ETag"`
	Body   *ProtocolEnvelope[ManifestDocument]
}
type ManifestVersionsInput struct {
	TeamPath
	Cursor string `query:"cursor" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type ManifestVersions struct {
	Items      []ManifestVersionView `json:"items"`
	HasMore    bool                  `json:"has_more"`
	NextCursor string                `json:"next_cursor,omitempty"`
}
type CapabilityInput struct {
	AgentPath
	ProjectID       string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ManifestVersion string `query:"manifest_version" pattern:"^[0-9]+[.][0-9]+[.][0-9]+$" maxLength:"32"`
}
type CapabilityQuota struct {
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Remaining *int64 `json:"remaining,omitempty"`
}
type EffectiveOperation struct {
	Name              string          `json:"name"`
	Status            string          `json:"status" enum:"AVAILABLE,CAPABILITY_NOT_AUTHORIZED,QUOTA_EXHAUSTED,NOT_APPLICABLE"`
	ReasonCode        string          `json:"reason_code,omitempty"`
	Via               string          `json:"via,omitempty"`
	HumanConfirmation string          `json:"human_confirmation"`
	QuotaState        CapabilityQuota `json:"quota_state"`
}
type EffectiveCapabilities struct {
	ManifestVersion string               `json:"manifest_version"`
	ProjectID       *guid.ID             `json:"project_id,omitempty"`
	Operations      []EffectiveOperation `json:"operations"`
}

func (m *Module) registerManifestHTTP(api huma.API) {
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}
	}
	read := op("agent_manifest.get", "/agent-operation-manifest", "Read an immutable agent operation manifest by version")
	read.Errors = append(read.Errors, 304)
	authz.RegisterTenantMember(m.registrar, api, read, m.manifestVersionHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_manifest.versions", "/agent-operation-manifest-versions", "List immutable agent operation manifest publications"), m.manifestVersionsHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_capabilities.get", agentPath+"/capabilities", "Preview current agent capabilities without reserving quota"), m.capabilitiesHTTP)
}
func (m *Module) manifestVersionHTTP(ctx context.Context, in *ManifestReadInput) (*ManifestReadOutput, error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	result, err := readHTTP(ctx, m, func(tx *gorm.DB) (ManifestDocument, error) { return m.manifestOn(ctx, tx, in.Version) })
	if err != nil {
		return nil, err
	}
	doc := result.Body.Data
	out := &ManifestReadOutput{Status: 200, ETag: `"` + doc.SHA256 + `"`, Body: &result.Body}
	if etagMatches(in.IfNoneMatch, doc.SHA256) {
		out.Status = 304
		out.Body = nil
	}
	return out, nil
}
func (m *Module) manifestVersionsHTTP(ctx context.Context, in *ManifestVersionsInput) (*ProtocolBody[ManifestVersions], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (ManifestVersions, error) {
		q := query.BindManifestVersion(tx, m.names)
		read := q.WithContext(ctx)
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		if limit < 1 || limit > 100 {
			return ManifestVersions{}, ErrInvalid
		}
		if in.Cursor != "" {
			cursor, err := guid.RequireID(in.Cursor)
			if err != nil {
				return ManifestVersions{}, ErrInvalid
			}
			read = read.Where(q.ID.Lt(cursor))
		}
		rows, err := read.Order(q.ID.Desc()).Limit(limit + 1).Find()
		out := ManifestVersions{Items: []ManifestVersionView{}}
		if err != nil {
			return out, err
		}
		if len(rows) > limit {
			out.HasMore = true
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		for _, row := range rows {
			out.Items = append(out.Items, manifestVersionView(row))
		}
		return out, nil
	})
}
func (m *Module) capabilitiesHTTP(ctx context.Context, in *CapabilityInput) (*ProtocolBody[EffectiveCapabilities], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	agent, err := guid.RequireID(in.AgentPrincipalID)
	if err != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, err = guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, agentHTTPError(ctx, ErrInvalid)
		}
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (EffectiveCapabilities, error) {
		return m.capabilitiesOn(ctx, tx, tenantID(ctx), agent, project, in.ManifestVersion)
	})
}
func (m *Module) capabilitiesOn(ctx context.Context, tx *gorm.DB, tenant, agent, project guid.ID, version string) (EffectiveCapabilities, error) {
	out := EffectiveCapabilities{Operations: []EffectiveOperation{}}
	permission := "agent_read"
	if actorID(ctx) == agent {
		permission = "SELF"
	}
	identity, err := m.authorize(ctx, tx, tenant, agent, permission, false)
	if err != nil {
		return out, err
	}
	doc, err := m.manifestOn(ctx, tx, version)
	if err != nil {
		return out, err
	}
	out.ManifestVersion = doc.ManifestVersion
	if project > 0 {
		out.ProjectID = &project
		if m.capabilityExplain == nil {
			return out, ErrUnavailable
		}
		access, err := m.capabilityExplain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: agent, Permission: "project_read"})
		if err != nil {
			return out, err
		}
		if !access.Allowed {
			return out, ErrForbidden
		}
	}
	current := map[string]Operation{}
	for _, operation := range m.registry.List() {
		current[operation.Name] = operation
	}
	for _, op := range doc.Operations {
		item := EffectiveOperation{Name: op.Name, Status: "NOT_APPLICABLE", ReasonCode: "RESOURCE_CONTEXT_REQUIRED", Via: op.Via, HumanConfirmation: op.HumanConfirmation, QuotaState: CapabilityQuota{Kind: op.Quota, State: "NOT_APPLICABLE"}}
		if identity.State != "ACTIVE" {
			item.Status = "CAPABILITY_NOT_AUTHORIZED"
			item.ReasonCode = "PRINCIPAL_INACTIVE"
		} else if op.HumanOnly {
			item.Status = "CAPABILITY_NOT_AUTHORIZED"
			item.ReasonCode = "AGENT_PROHIBITED"
		} else if op.Via == "SELF" {
			item.Status = "AVAILABLE"
			item.ReasonCode = ""
		} else if m.capabilityExplain == nil {
			item.ReasonCode = "AUTHORITY_UNAVAILABLE"
		} else {
			explanation, err := m.capabilityExplain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: agent, Permission: op.RequiredPermission})
			if err != nil {
				return out, err
			}
			if explanation.Allowed && op.Via != "RELATION" {
				item.Status = "AVAILABLE"
				item.ReasonCode = ""
			}
			if !explanation.Allowed {
				item.Status = "CAPABILITY_NOT_AUTHORIZED"
				item.ReasonCode = explanation.Reason
				if item.ReasonCode == "" {
					item.ReasonCode = "MISSING_PERMISSION"
				}
			}
		}
		// A self operation addressing a particular reminder still needs its resource
		// ownership/state, which this project-level preview cannot truthfully assert.
		for _, p := range op.Parameters {
			if (item.Status == "AVAILABLE" || item.Status == "NOT_APPLICABLE") && p.In == "path" && p.Name != "tenant_id" && p.Name != "agent_principal_id" {
				item.Status = "NOT_APPLICABLE"
				item.ReasonCode = "RESOURCE_CONTEXT_REQUIRED"
			}
		}
		if op.Name == "agent_reminders.create" {
			q := query.BindReminder(tx, m.names)
			count, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.State.In("ACTIVE", "PAUSED")).Count()
			if err != nil {
				return out, err
			}
			remaining := int64(reminderLimit) - count
			if remaining < 0 {
				remaining = 0
			}
			state := "AVAILABLE"
			if remaining == 0 {
				state = "EXHAUSTED"
				if item.Status == "AVAILABLE" {
					item.Status = "QUOTA_EXHAUSTED"
					item.ReasonCode = "REMINDER_LIMIT"
				}
			}
			item.QuotaState = CapabilityQuota{Kind: "RATE_LIMIT", State: state, Remaining: &remaining}
		} else if op.Quota != "NONE" && op.Quota != "" {
			item.QuotaState = CapabilityQuota{Kind: op.Quota, State: "CONTEXT_REQUIRED"}
			if item.Status == "AVAILABLE" {
				item.Status = "NOT_APPLICABLE"
				item.ReasonCode = "QUOTA_CONTEXT_REQUIRED"
			}
		}
		if _, exists := current[op.Name]; !exists {
			item.Status = "NOT_APPLICABLE"
			item.ReasonCode = "OPERATION_UNAVAILABLE"
		}
		// A route can contain placeholders even when a third-party descriptor did
		// not include its parameter schema. Never infer a concrete resource from it.
		path := strings.ReplaceAll(op.Path, "{tenant_id}", "")
		path = strings.ReplaceAll(path, "{agent_principal_id}", "")
		if project > 0 {
			path = strings.ReplaceAll(path, "{project_id}", "")
		}
		if strings.Contains(path, "{") && item.Status == "AVAILABLE" {
			item.Status = "NOT_APPLICABLE"
			item.ReasonCode = "RESOURCE_CONTEXT_REQUIRED"
		}
		out.Operations = append(out.Operations, item)
	}
	return out, nil
}
