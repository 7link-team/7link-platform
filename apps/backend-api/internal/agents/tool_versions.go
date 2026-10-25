package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode/utf8"
)

type ToolLocalSource struct {
	MachineID guid.ID `json:"machine_id"`
	Runtime   string  `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Name      string  `json:"name" minLength:"1" maxLength:"200"`
	Scope     string  `json:"scope" maxLength:"64"`
}
type ToolPreset struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Transport   string `json:"transport"`
	Endpoint    string `json:"endpoint"`
	ContentHash string `json:"content_hash"`
}

// Presets contain public package templates only. Import never grants a tool.
func toolPresets() []ToolPreset {
	return []ToolPreset{
		{Key: "browser-automation", Title: "Browser automation", Transport: "STDIO_PACKAGE", Endpoint: "npm:@playwright/mcp@0.0.40"},
		{Key: "issue-tracker", Title: "Issue tracker", Transport: "STREAMABLE_HTTP"},
		{Key: "code-hosting", Title: "Code hosting", Transport: "STREAMABLE_HTTP"},
		{Key: "error-monitoring", Title: "Error monitoring", Transport: "STREAMABLE_HTTP"},
	}
}
func (m *Module) prepareToolSourceOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in *ToolInput) (string, string, error) {
	if in.PresetKey != "" && in.FromLocal != nil {
		return "", "", ErrInvalid
	}
	if in.PresetKey != "" {
		for _, p := range toolPresets() {
			if p.Key == in.PresetKey {
				if in.Kind != "MCP_SERVER" {
					return "", "", ErrInvalid
				}
				if in.Transport == "" {
					in.Transport = p.Transport
				}
				if in.Endpoint == "" {
					in.Endpoint = p.Endpoint
				}
				return "PRESET", p.Key, nil
			}
		}
		return "", "", ErrInvalid
	}
	if in.FromLocal != nil {
		src := in.FromLocal
		if in.Kind != "MCP_SERVER" || src.MachineID <= 0 || src.Name == "" || (src.Runtime != "CODEX" && src.Runtime != "CLAUDE_CODE") {
			return "", "", ErrInvalid
		}
		if m.toolSourceFacts.ReadOn == nil {
			return "", "", ErrUnavailable
		}
		facts, err := m.toolSourceFacts.ReadOn(ctx, tx, tenant, src.MachineID)
		if err != nil {
			return "", "", ErrForbidden
		}
		for _, inspection := range facts.Discoveries {
			if src.Scope != "" && inspection.ScopeKey != src.Scope {
				continue
			}
			for _, runtime := range inspection.Discovery {
				if runtime.Runtime != src.Runtime || runtime.State != "READY" {
					continue
				}
				for _, name := range runtime.MCP {
					if name == src.Name {
						return "MACHINE", fmt.Sprintf("%s:%s:%s:%s", src.MachineID.String(), src.Runtime, inspection.ScopeKey, src.Name), nil
					}
				}
			}
		}
		return "", "", ErrNotFound
	}
	return "MANUAL", "", nil
}

type ToolVersionView struct {
	ConfigurationHash string    `json:"content_sha256"`
	ID                guid.ID   `json:"id"`
	VersionNo         int       `json:"version_no"`
	Transport         string    `json:"transport"`
	Endpoint          string    `json:"endpoint"`
	ContentHash       string    `json:"content_hash"`
	SkillBody         string    `json:"skill_body,omitempty"`
	State             string    `json:"state"`
	Source            string    `json:"source"`
	SourceRef         string    `json:"source_ref"`
	ChangeNote        string    `json:"change_note"`
	PublishedBy       guid.ID   `json:"published_by"`
	PublishedAt       time.Time `json:"published_at"`
}

func toolVersionView(r *model.ToolVersion) ToolVersionView {
	hash := r.ContentHash
	if r.Transport != "" {
		hash = toolConfigurationHash(r.Transport, r.Endpoint, r.ContentHash)
	}
	return ToolVersionView{ConfigurationHash: hash, ID: r.ID, VersionNo: r.VersionNo, Transport: r.Transport, Endpoint: r.Endpoint, ContentHash: r.ContentHash, SkillBody: r.SkillBody, State: r.State, Source: r.Source, SourceRef: r.SourceRef, ChangeNote: r.ChangeNote, PublishedBy: r.PublishedBy, PublishedAt: r.CreatedAt}
}

type ToolPublishInput struct {
	Version     int    `json:"version" minimum:"1"`
	Transport   string `json:"transport" enum:"STREAMABLE_HTTP,STDIO_PACKAGE"`
	Endpoint    string `json:"endpoint" maxLength:"512"`
	ContentHash string `json:"content_hash" maxLength:"64"`
	ChangeNote  string `json:"change_note" maxLength:"2000"`
}
type ToolStateChange struct {
	Version      int    `json:"version" minimum:"1"`
	State        string `json:"state,omitempty" enum:",ACTIVE,DISABLED,ARCHIVED"`
	VersionNo    int    `json:"version_no,omitempty" minimum:"0"`
	VersionState string `json:"version_state,omitempty" enum:",REVOKED"`
	Reason       string `json:"reason,omitempty" maxLength:"2000"`
}
type ToolUpstreamInput struct {
	Version     int    `json:"version" minimum:"1"`
	ContentHash string `json:"content_hash" minLength:"64" maxLength:"64"`
	Note        string `json:"note" minLength:"10" maxLength:"2000"`
}
type ToolUpstreamView struct {
	ID          guid.ID   `json:"id"`
	ContentHash string    `json:"content_hash"`
	Note        string    `json:"note"`
	ReportedBy  guid.ID   `json:"reported_by"`
	ReportedAt  time.Time `json:"reported_at"`
}
type ToolDetail struct {
	Entry            ToolView           `json:"entry"`
	CurrentVersionNo int                `json:"current_version_no"`
	Versions         []ToolVersionView  `json:"versions"`
	Upstream         []ToolUpstreamView `json:"upstream"`
	UpstreamChanged  bool               `json:"upstream_changed"`
}

func (m *Module) toolLockedOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, expected int) (*model.ToolEntry, error) {
	if tool <= 0 {
		return nil, ErrInvalid
	}
	if err := m.teamToolAuthority(ctx, tx, tenant); err != nil {
		return nil, err
	}
	q := query.BindToolEntry(tx, m.names)
	r, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool)).First()
	if err != nil {
		return nil, err
	}
	if expected > 0 && r.Version != expected {
		return nil, ErrConflict
	}
	return r, nil
}
func (m *Module) insertToolVersionOn(ctx context.Context, tx *gorm.DB, entry *model.ToolEntry, no int, source, ref, note string) (ToolVersionView, error) {
	if m.ids == nil {
		return ToolVersionView{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return ToolVersionView{}, err
	}
	if id <= 0 {
		return ToolVersionView{}, ErrUnavailable
	}
	r := model.ToolVersion{ID: guid.ID(id), TenantID: entry.TenantID, ToolID: entry.ID, VersionNo: no, Transport: entry.Transport, Endpoint: entry.Endpoint, ContentHash: entry.ContentHash, SkillBody: entry.SkillBody, State: "ACTIVE", Source: source, SourceRef: ref, ChangeNote: note, PublishedBy: actorID(ctx), CreatedAt: m.now()}
	err = query.BindToolVersion(tx, m.names).WithContext(ctx).Create(&r)
	return toolVersionView(&r), err
}
func (m *Module) toolDetailOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID) (ToolDetail, error) {
	r, err := m.toolLockedOn(ctx, tx, tenant, tool, 0)
	if err != nil {
		return ToolDetail{}, err
	}
	out := ToolDetail{Entry: toolView(r), Versions: []ToolVersionView{}, Upstream: []ToolUpstreamView{}}
	v := query.BindToolVersion(tx, m.names)
	rows, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(tool)).Order(v.VersionNo.Desc()).Find()
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		out.Versions = append(out.Versions, toolVersionView(row))
		if out.CurrentVersionNo == 0 && row.State == "ACTIVE" {
			out.CurrentVersionNo = row.VersionNo
		}
	}
	u := query.BindToolUpstream(tx, m.names)
	reports, err := u.WithContext(ctx).Where(u.TenantID.Eq(tenant), u.ToolID.Eq(tool)).Order(u.ID.Desc()).Limit(20).Find()
	if err != nil {
		return out, err
	}
	for _, row := range reports {
		out.Upstream = append(out.Upstream, ToolUpstreamView{ID: row.ID, ContentHash: row.ContentHash, Note: row.Note, ReportedBy: row.ReportedBy, ReportedAt: row.CreatedAt})
	}
	currentHash := r.ContentHash
	if r.Kind == "MCP_SERVER" {
		currentHash = toolConfigurationHash(r.Transport, r.Endpoint, r.ContentHash)
	}
	out.UpstreamChanged = len(reports) > 0 && reports[0].ContentHash != currentHash
	return out, nil
}
func (m *Module) publishToolOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, in ToolPublishInput) (ToolVersionView, error) {
	if in.Version < 1 || len(in.ChangeNote) > 2000 {
		return ToolVersionView{}, ErrInvalid
	}
	r, err := m.toolLockedOn(ctx, tx, tenant, tool, in.Version)
	if err != nil {
		return ToolVersionView{}, err
	}
	if r.State == "ARCHIVED" || r.Kind != "MCP_SERVER" {
		return ToolVersionView{}, ErrConflict
	}
	config, err := validateToolConfiguration(ToolInput{EntryKey: r.EntryKey, Kind: r.Kind, Title: r.Title, Transport: in.Transport, Endpoint: in.Endpoint, ContentHash: in.ContentHash})
	if err != nil {
		return ToolVersionView{}, err
	}
	if config.Transport == "STDIO_PACKAGE" && r.CredentialSealed != "" {
		return ToolVersionView{}, ErrInvalid
	}
	if config.Transport == r.Transport && config.Endpoint == r.Endpoint && (config.Transport == "STREAMABLE_HTTP" || config.ContentHash == r.ContentHash) {
		return ToolVersionView{}, ErrConflict
	}
	v := query.BindToolVersion(tx, m.names)
	last, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(tool)).Order(v.VersionNo.Desc()).First()
	if err != nil {
		return ToolVersionView{}, err
	}
	r.Transport = config.Transport
	r.Endpoint = config.Endpoint
	r.ContentHash = config.ContentHash
	out, err := m.insertToolVersionOn(ctx, tx, r, last.VersionNo+1, last.Source, last.SourceRef, in.ChangeNote)
	if err != nil {
		return out, err
	}
	q := query.BindToolEntry(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool), q.Version.Eq(in.Version)).Updates(map[string]any{"transport": r.Transport, "endpoint": r.Endpoint, "content_hash": r.ContentHash, "version": in.Version + 1})
	if err != nil {
		return out, err
	}
	if res.RowsAffected != 1 {
		return out, ErrConflict
	}
	return out, m.recordToolChange(ctx, tx, tenant, tool, "TOOL_VERSION_PUBLISHED", in.ChangeNote, in.Version+1, out.VersionNo)
}
func (m *Module) changeToolStateOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, in ToolStateChange) (ToolView, error) {
	if in.Version < 1 || len(in.Reason) > 2000 {
		return ToolView{}, ErrInvalid
	}
	r, err := m.toolLockedOn(ctx, tx, tenant, tool, in.Version)
	if err != nil {
		return ToolView{}, err
	}
	if r.State == "ARCHIVED" {
		return ToolView{}, ErrConflict
	}
	updates := map[string]any{"version": r.Version + 1}
	v := query.BindToolVersion(tx, m.names)
	if in.VersionNo > 0 {
		if in.State != "" || in.VersionState != "REVOKED" || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || r.Kind != "MCP_SERVER" {
			return ToolView{}, ErrInvalid
		}
		result, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(tool), v.VersionNo.Eq(in.VersionNo), v.State.Eq("ACTIVE")).Update(v.State, "REVOKED")
		if e != nil {
			return ToolView{}, e
		}
		if result.RowsAffected != 1 {
			return ToolView{}, ErrConflict
		}
		current, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(tool), v.State.Eq("ACTIVE")).Order(v.VersionNo.Desc()).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			r.State = "DISABLED"
		} else if e != nil {
			return ToolView{}, e
		} else {
			r.Transport = current.Transport
			r.Endpoint = current.Endpoint
			r.ContentHash = current.ContentHash
			updates["transport"] = r.Transport
			updates["endpoint"] = r.Endpoint
			updates["content_hash"] = r.ContentHash
		}
	} else {
		if in.VersionState != "" || (in.State != "ACTIVE" && in.State != "DISABLED" && in.State != "ARCHIVED") {
			return ToolView{}, ErrInvalid
		}
		if in.State != "ACTIVE" && utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 {
			return ToolView{}, ErrInvalid
		}
		if in.State == "ARCHIVED" && r.State == "ACTIVE" {
			return ToolView{}, ErrConflict
		}
		if in.State == "ACTIVE" {
			count, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ToolID.Eq(tool), v.State.Eq("ACTIVE")).Count()
			if e != nil {
				return ToolView{}, e
			}
			if count == 0 {
				return ToolView{}, ErrConflict
			}
		}
		r.State = in.State
	}
	updates["state"] = r.State
	q := query.BindToolEntry(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool), q.Version.Eq(in.Version)).Updates(updates)
	if err != nil {
		return ToolView{}, err
	}
	if res.RowsAffected != 1 {
		return ToolView{}, ErrConflict
	}
	r.Version++
	if err = m.recordToolChange(ctx, tx, tenant, tool, "TOOL_STATE_CHANGED", in.Reason, r.Version, in.VersionNo); err != nil {
		return ToolView{}, err
	}
	return toolView(r), nil
}
func (m *Module) reportToolUpstreamOn(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, in ToolUpstreamInput) (ToolDetail, error) {
	if in.Version < 1 || len(in.ContentHash) != 64 || len(in.Note) > 2000 || utf8.RuneCountInString(strings.TrimSpace(in.Note)) < 10 {
		return ToolDetail{}, ErrInvalid
	}
	if _, err := hex.DecodeString(in.ContentHash); err != nil {
		return ToolDetail{}, ErrInvalid
	}
	r, err := m.toolLockedOn(ctx, tx, tenant, tool, in.Version)
	if err != nil {
		return ToolDetail{}, err
	}
	if r.State == "ARCHIVED" {
		return ToolDetail{}, ErrConflict
	}
	if m.ids == nil {
		return ToolDetail{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return ToolDetail{}, err
	}
	if id <= 0 {
		return ToolDetail{}, ErrUnavailable
	}
	row := model.ToolUpstream{ID: guid.ID(id), TenantID: tenant, ToolID: tool, ContentHash: strings.ToLower(in.ContentHash), Note: in.Note, ReportedBy: actorID(ctx), CreatedAt: m.now()}
	if err = query.BindToolUpstream(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return ToolDetail{}, err
	}
	q := query.BindToolEntry(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(tool), q.Version.Eq(in.Version)).Update(q.Version, in.Version+1)
	if err != nil {
		return ToolDetail{}, err
	}
	if res.RowsAffected != 1 {
		return ToolDetail{}, ErrConflict
	}
	if err = m.recordToolChange(ctx, tx, tenant, tool, "TOOL_UPSTREAM_REPORTED", in.Note, in.Version+1, 0); err != nil {
		return ToolDetail{}, err
	}
	return m.toolDetailOn(ctx, tx, tenant, tool)
}

// Tool audit records preserve the human's decision and revision without credentials.
func (m *Module) recordToolChange(ctx context.Context, tx *gorm.DB, tenant, tool guid.ID, action, reason string, revision, versionNo int) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	if reason == "" {
		reason = "Team tool catalog command"
	}
	authority := "IAM:tool_catalog_manage"
	event := audit.NewDomainEvent(ctx, tenant, "agents.changed", "tool_entry", tool)
	event.Detail["action"] = action
	event.Detail["reason"] = reason
	event.Detail["authority_source"] = authority
	event.Detail["evidence_refs"] = []string{}
	event.Detail["revision"] = revision
	event.Detail["version_no"] = versionNo
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agents.changed", Actor: events.Actor{Kind: "HUMAN", ID: actorID(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "agent", ID: tool}, SubjectVersion: int64(revision), Data: map[string]any{"action": action, "entity_kind": "TOOL_ENTRY", "reason": reason, "version_no": versionNo}})
	return err
}
