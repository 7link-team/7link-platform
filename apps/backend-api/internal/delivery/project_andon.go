package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type projectAndonBody struct {
	Version int    `json:"version" minimum:"1"`
	Reason  string `json:"reason" minLength:"10" maxLength:"2000"`
}
type projectAndonHold struct {
	Kind   string    `json:"kind"`
	Source string    `json:"source,omitempty"`
	ID     guid.ID   `json:"id,omitempty"`
	Since  time.Time `json:"since"`
	Actor  guid.ID   `json:"actor,omitempty"`
}
type projectAndonView struct {
	Version    int                 `json:"version"`
	PullID     guid.ID             `json:"pull_id"`
	Held       bool                `json:"held"`
	Executions []ExecutionHoldFact `json:"executions"`
}

func (m *Module) noProjectAndonOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) bool {
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if err != nil {
		return false
	}
	var holds []projectAndonHold
	if json.Unmarshal([]byte(p.Holds), &holds) != nil {
		return false
	}
	return len(holds) == 0
}

func (m *Module) projectAndonAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, release bool) (*model.Project, string, error) {
	verifyFence, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, 0, actor)
	if err != nil {
		return nil, "", err
	}
	p, _, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_read")
	if err != nil {
		return nil, "", err
	}
	if err = verifyFence(); err != nil {
		return nil, "", err
	}
	source, err := m.andonAuthorityOn(ctx, tx, &model.WorkItem{TenantID: tenant, ProjectID: project, ID: project}, actor, release)
	return p, source, err
}

func (m *Module) projectAndonOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, in projectAndonBody, release bool) (projectAndonView, error) {
	var out projectAndonView
	reason := strings.TrimSpace(in.Reason)
	if in.Version < 1 || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) < 10 || utf8.RuneCountInString(reason) > 2000 {
		return out, ErrInvalidArgument
	}
	p, source, err := m.projectAndonAuthorityOn(ctx, tx, tenant, project, actor, release)
	if err != nil {
		return out, err
	}
	if p.Version != in.Version {
		return out, ErrWorkVersion
	}
	if p.ArchivedAt != nil || slices.Contains([]string{"CLOSED", "CANCELLED"}, p.Stage) { //nolint:misspell // Frozen protocol state.
		return out, ErrWorkProjectReadOnly
	}
	// Preserve every unrelated hold byte/field, including escalation provenance.
	var holds []map[string]json.RawMessage
	if json.Unmarshal([]byte(p.Holds), &holds) != nil {
		return out, ErrUnavailable
	}
	index := -1
	for i, h := range holds {
		if string(h["kind"]) == `"MANUAL"` && string(h["source"]) == `"ANDON"` {
			index = i
			if json.Unmarshal(h["id"], &out.PullID) != nil {
				return out, ErrUnavailable
			}
			break
		}
	}
	if release && index < 0 || !release && index >= 0 {
		return out, ErrResultState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if release {
		holds = slices.Delete(holds, index, index+1)
	} else {
		id, e := m.ids.Next()
		if e != nil {
			return out, e
		}
		out.PullID = guid.ID(id)
		encoded, _ := json.Marshal(projectAndonHold{Kind: "MANUAL", Source: "ANDON", ID: out.PullID, Since: now, Actor: actor})
		var hold map[string]json.RawMessage
		if json.Unmarshal(encoded, &hold) != nil {
			return out, ErrUnavailable
		}
		holds = append(holds, hold)
	}
	raw, err := json.Marshal(holds)
	if err != nil {
		return out, err
	}
	q := query.BindProjects(tx, m.service.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Version.Eq(in.Version)).Updates(map[string]any{"holds": string(raw), "version": p.Version + 1, "updated_at": now, "updated_by": actor})
	if err != nil {
		return out, err
	}
	if changed.RowsAffected != 1 {
		return out, ErrWorkVersion
	}
	if m.executionHolds.ChangeOn != nil {
		// A project release cannot resume an execution held by work ANDON.
		w := query.BindWorkItems(tx, m.service.names)
		works, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ArchivedAt.IsNull()).Order(w.ID).Find()
		if e != nil {
			return out, e
		}
		for _, work := range works {
			releaseRuntime := release && len(holds) == 0 && work.PausedAt == nil
			if releaseRuntime {
				if e := m.requireNoAndonOn(ctx, tx, work); e != nil {
					if !errors.Is(e, ErrAndonBlocked) {
						return out, e
					}
					releaseRuntime = false
				}
			}
			facts, e := m.executionHolds.ChangeOn(ctx, tx, tenant, project, work.ID, releaseRuntime)
			if e != nil {
				return out, e
			}
			out.Executions = append(out.Executions, facts...)
		}
	}
	out.Version, out.Held = p.Version+1, !release
	typ := "project.andon_pulled"
	if release {
		typ = "project.andon_cleared"
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.project_andon", "project", project)
	event.Detail["reason"], event.Detail["pull_id"], event.Detail["held"] = reason, out.PullID.String(), !release
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	kind, err := m.resultActorKindOn(ctx, tx, actor, source)
	if err != nil {
		return out, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: typ, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(out.Version), Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: source}, Data: map[string]any{"pull_id": out.PullID.String(), "target_kind": "PROJECT", "target_id": project.String(), "reason": reason}})
	return out, err
}
