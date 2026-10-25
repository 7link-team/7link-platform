package agents

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type RosterInput struct {
	TeamPath
	Limit         int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset        int    `query:"offset" default:"0" minimum:"0"`
	Search        string `query:"search" maxLength:"200"`
	Kind          string `query:"kind" enum:"HUMAN,AGENT"`
	Role          string `query:"role" maxLength:"200"`
	State         string `query:"state" maxLength:"200"`
	ProjectID     string `query:"project_id" maxLength:"19"`
	MachineID     string `query:"machine_id" maxLength:"19"`
	Runtime       string `query:"runtime_kind" enum:"CODEX,CLAUDE_CODE"`
	Model         string `query:"model_id" maxLength:"160"`
	Origin        string `query:"origin" enum:"HUMAN_CREATED,AGENT_SPAWNED"`
	ParentAgentID string `query:"parent_agent_id" maxLength:"19"`
	SpawnGrantID  string `query:"spawn_grant_id" maxLength:"19"`
	Lineage       string `query:"lineage_status" enum:"HUMAN_CREATED,SPAWNED,ADOPTED,ORPHANED"`
	Activity      string `query:"activity_status" maxLength:"100"`
	Sort          string `query:"sort" default:"name" enum:"name,-name,created_at,-created_at,state,-state,fpa_30d,-fpa_30d,load_ratio,-load_ratio,kind,-kind,origin,-origin"`
}
type RosterWork struct {
	ID        guid.ID `json:"id"`
	ProjectID guid.ID `json:"project_id"`
	Title     string  `json:"title"`
	State     string  `json:"state"`
}
type RosterLoad struct {
	ActiveItems int  `json:"active_items"`
	Max         *int `json:"max,omitempty"`
}
type RosterEntry struct {
	Kind                 string          `json:"kind"`
	PrincipalID          guid.ID         `json:"principal_id"`
	AgentID              *guid.ID        `json:"agent_id,omitempty"`
	Name                 string          `json:"name"`
	Handle               string          `json:"handle,omitempty"`
	Avatar               string          `json:"avatar,omitempty"`
	Roles                []string        `json:"roles"`
	State                string          `json:"state"`
	ProcessStatus        string          `json:"process_status,omitempty"`
	Runtime              string          `json:"runtime,omitempty"`
	Model                string          `json:"model,omitempty"`
	MachineID            *guid.ID        `json:"machine_id,omitempty"`
	Origin               string          `json:"origin,omitempty"`
	Lineage              string          `json:"lineage_status,omitempty"`
	ParentAgentID        *guid.ID        `json:"parent_agent_id,omitempty"`
	ExpiresAt            *time.Time      `json:"expires_at,omitempty"`
	Activity             string          `json:"activity_status,omitempty"`
	Load                 RosterLoad      `json:"load"`
	CurrentWork          []RosterWork    `json:"current_work_items"`
	LastActiveAt         *time.Time      `json:"last_active_at"`
	Stats                RosterAnalytics `json:"stats_30d"`
	FPA                  *float64        `json:"fpa_30d"`
	PerformanceAvailable bool            `json:"performance_available"`
	CreatedAt            time.Time       `json:"created_at"`
}
type RosterPage struct {
	Items   []RosterEntry `json:"items"`
	Total   int           `json:"total"`
	HasMore bool          `json:"has_more"`
	Scope   string        `json:"scope"`
}

func (m *Module) injectRoster(s *module.Services) error {
	if v, e := module.Resolve[core.TeamRoster](s); e == nil {
		m.teamRoster = v
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	if v, e := module.Resolve[identity.TeamRosterPrincipals](s); e == nil {
		m.rosterPrincipals = v
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	return nil
}
func (m *Module) rosterOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in RosterInput) (RosterPage, error) {
	out := RosterPage{Items: []RosterEntry{}, Scope: "CURRENT_READER_VISIBLE_WORK_AND_DECISIONS"}
	if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 || !utf8.ValidString(in.Search) || utf8.RuneCountInString(in.Search) > 200 {
		return out, ErrInvalid
	}
	if m.principals.LockActiveRow == nil || m.members == nil || m.teamRoster.ListOn == nil || m.rosterPrincipals.ReadOn == nil {
		return out, ErrUnavailable
	}
	p, active, e := m.principals.LockActiveRow(ctx, tx, actorID(ctx))
	if e != nil {
		return out, e
	}
	if !active || p.ID != actorID(ctx) || tenantID(ctx) != tenant {
		return out, ErrForbidden
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actorID(ctx))
	if e != nil {
		return out, e
	}
	if !active {
		return out, ErrForbidden
	}
	ids := []guid.ID{}
	for _, raw := range []string{in.ProjectID, in.MachineID, in.ParentAgentID, in.SpawnGrantID} {
		id, e := position(raw)
		if e != nil {
			return out, e
		}
		ids = append(ids, id)
	}
	projectMembers := map[guid.ID][]string{}
	if ids[0] > 0 {
		if m.policy == nil {
			return out, ErrUnavailable
		}
		access, err := m.policy(ctx, tx, tenant, ids[0], "project_read", actorID(ctx))
		if err != nil {
			return out, err
		}
		if !access.AllowsEntityRows(ids[0]) {
			return out, ErrForbidden
		}
		if m.teamRoster.ProjectOn == nil {
			return out, ErrUnavailable
		}
		projectMembers, e = m.teamRoster.ProjectOn(ctx, tx, tenant, ids[0])
		if e != nil {
			return out, e
		}
	}
	entries := []RosterEntry{}
	if in.Kind != "AGENT" && in.MachineID == "" && in.Runtime == "" && in.Model == "" && in.Origin == "" && in.ParentAgentID == "" && in.SpawnGrantID == "" && in.Lineage == "" && in.Activity == "" {
		for offset := 0; ; offset += 200 {
			page, e := m.teamRoster.ListOn(ctx, tx, tenant, 200, offset, "")
			if e != nil {
				return out, e
			}
			batch := make([]guid.ID, 0, len(page.Items))
			for _, row := range page.Items {
				batch = append(batch, row.PrincipalID)
			}
			people, e := m.rosterPrincipals.ReadOn(ctx, tx, batch)
			if e != nil {
				return out, e
			}
			for _, row := range page.Items {
				person, ok := people[row.PrincipalID]
				if !ok || person.Kind != "HUMAN" {
					continue
				}
				state := strings.ToUpper(row.State)
				if in.State != "" && !slices.Contains(strings.Split(in.State, ","), state) {
					continue
				}
				roles := row.Roles
				if ids[0] > 0 {
					var ok bool
					roles, ok = projectMembers[row.PrincipalID]
					if !ok {
						continue
					}
				}
				if in.Role != "" && !slices.Contains(roles, in.Role) {
					continue
				}
				name := row.Name
				if name == "" {
					name = person.Name
				}
				if !strings.Contains(strings.ToLower(name), strings.ToLower(strings.TrimSpace(in.Search))) {
					continue
				}
				entries = append(entries, RosterEntry{Kind: "HUMAN", PrincipalID: row.PrincipalID, Name: name, Roles: roles, State: state, CurrentWork: []RosterWork{}, LastActiveAt: person.LastActiveAt, CreatedAt: row.CreatedAt})
			}
			if !page.HasMore {
				break
			}
		}
	}
	if in.Kind != "HUMAN" {
		if m.profiles.DirectoryOn == nil {
			return out, ErrUnavailable
		}
		filter := fleet.FleetRosterFilter{Search: strings.TrimSpace(in.Search), Role: in.Role, State: in.State, Runtime: in.Runtime, Model: in.Model, Origin: in.Origin, Lineage: in.Lineage, MachineID: ids[1], ParentAgentID: ids[2], SpawnGrantID: ids[3]}
		for offset := 0; ; offset += 200 {
			page, e := m.profiles.DirectoryOn(ctx, tx, tenant, filter, 200, offset)
			if e != nil {
				return out, profileError(e)
			}
			for _, row := range page.Items {
				if _, e := m.authorize(ctx, tx, tenant, row.PrincipalID, "agent_read", true); errors.Is(e, ErrForbidden) {
					continue
				} else if e != nil {
					return out, e
				}
				if ids[0] > 0 {
					if _, ok := projectMembers[row.PrincipalID]; !ok {
						continue
					}
				}
				activity := "OFFLINE"
				if row.State == "ACTIVE" && row.ProcessStatus == "RUNNING" && row.MachineOnline {
					activity = "ONLINE"
					q := query.BindLiveState(tx, m.names)
					live, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(row.PrincipalID)).First()
					if e == nil {
						activity = live.Activity
					} else if !errors.Is(e, gorm.ErrRecordNotFound) {
						return out, e
					}
				}
				if row.State == "CRASHED" {
					activity = "ERROR"
				}
				if in.Activity != "" && !slices.Contains(strings.Split(in.Activity, ","), activity) {
					continue
				}
				entry := RosterEntry{Kind: "AGENT", PrincipalID: row.PrincipalID, AgentID: &row.ID, Name: row.Name, Handle: row.Handle, Roles: row.Roles, State: row.State, ProcessStatus: row.ProcessStatus, Runtime: row.Runtime, Model: row.Model, MachineID: row.MachineID, Origin: row.Origin, Lineage: row.Lineage, ParentAgentID: row.ParentAgentID, ExpiresAt: row.ExpiresAt, Activity: activity, Load: RosterLoad{Max: &row.MaxParallel}, CurrentWork: []RosterWork{}, LastActiveAt: row.LastActiveAt, CreatedAt: row.CreatedAt}
				entries = append(entries, entry)
			}
			if !page.HasMore {
				break
			}
		}
	}
	for index := range entries {
		entry := &entries[index]
		if m.selectedWork.ListOn == nil {
			return out, ErrUnavailable
		}
		cursor := ""
		for {
			page, e := m.selectedWork.ListOn(ctx, tx, tenant, entry.PrincipalID, cursor, 100)
			if e != nil {
				return out, e
			}
			for _, work := range page.Items {
				if slices.Contains([]string{"ACCEPTED", "SKIPPED", "CANCELLED"}, work.State) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
					continue
				}
				if ids[0] > 0 && work.ProjectID != ids[0] {
					continue
				}
				entry.Load.ActiveItems++
				if len(entry.CurrentWork) < 3 {
					entry.CurrentWork = append(entry.CurrentWork, RosterWork{ID: work.ID, ProjectID: work.ProjectID, Title: work.Title, State: work.State})
				}
			}
			if !page.HasMore {
				break
			}
			if page.NextCursor == "" || page.NextCursor == cursor {
				return out, ErrUnavailable
			}
			cursor = page.NextCursor
		}
	}
	if m.rosterAnalytics != nil {
		for start := 0; start < len(entries); start += 200 {
			end := min(start+200, len(entries))
			batch := make([]guid.ID, 0, end-start)
			for _, entry := range entries[start:end] {
				batch = append(batch, entry.PrincipalID)
			}
			stats, e := m.rosterAnalytics(ctx, tx, tenant, batch)
			if e != nil {
				return out, e
			}
			for i := start; i < end; i++ {
				entry := &entries[i]
				entry.Stats = stats[entry.PrincipalID]
				entry.PerformanceAvailable = true
				if entry.Stats.FirstDecisions >= 5 {
					rate := float64(entry.Stats.FirstPassAccepts) / float64(entry.Stats.FirstDecisions)
					entry.FPA = &rate
				}
			}
		}
	}
	if strings.TrimPrefix(in.Sort, "-") == "fpa_30d" && m.rosterAnalytics == nil {
		return out, ErrUnavailable
	}
	if e := sortRoster(entries, in.Sort); e != nil {
		return out, e
	}
	out.Total = len(entries)
	if in.Offset >= len(entries) {
		return out, nil
	}
	end := min(in.Offset+in.Limit, len(entries))
	out.Items = entries[in.Offset:end]
	out.HasMore = end < len(entries)
	return out, nil
}

func sortRoster(entries []RosterEntry, order string) error {
	key := strings.TrimPrefix(order, "-")
	if key == "" {
		key = "name"
	}
	if !slices.Contains([]string{"name", "created_at", "state", "fpa_30d", "load_ratio", "kind", "origin"}, key) {
		return ErrInvalid
	}
	desc := strings.HasPrefix(order, "-")
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		cmp := 0
		switch key {
		case "name":
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "kind":
			cmp = strings.Compare(a.Kind, b.Kind)
		case "origin":
			cmp = strings.Compare(a.Origin, b.Origin)
		case "state":
			cmp = strings.Compare(a.State, b.State)
		case "created_at":
			cmp = a.CreatedAt.Compare(b.CreatedAt)
		case "load_ratio":
			left, right := float64(a.Load.ActiveItems), float64(b.Load.ActiveItems)
			if a.Load.Max != nil && *a.Load.Max > 0 {
				left /= float64(*a.Load.Max)
			}
			if b.Load.Max != nil && *b.Load.Max > 0 {
				right /= float64(*b.Load.Max)
			}
			if left < right {
				cmp = -1
			} else if left > right {
				cmp = 1
			}
		case "fpa_30d":
			if a.FPA == nil {
				if b.FPA == nil {
					return a.PrincipalID < b.PrincipalID
				}
				return false
			}
			if b.FPA == nil {
				return true
			}
			if *a.FPA < *b.FPA {
				cmp = -1
			} else if *a.FPA > *b.FPA {
				cmp = 1
			}
		}
		if cmp == 0 {
			return a.PrincipalID < b.PrincipalID
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
	return nil
}
func (m *Module) rosterHTTP(ctx context.Context, in *RosterInput) (*ProtocolBody[RosterPage], error) {
	if in.TenantID != tenantID(ctx).String() {
		return nil, agentHTTPError(ctx, ErrForbidden)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (RosterPage, error) { return m.rosterOn(ctx, tx, tenantID(ctx), *in) })
}
func (m *Module) registerRosterHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "team_roster.list", Method: http.MethodGet, Path: "/roster-entries", Summary: "Browse current Human and Agent roster with visible work and measured quality", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}, m.rosterHTTP)
}
