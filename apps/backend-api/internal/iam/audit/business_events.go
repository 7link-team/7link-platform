package audit

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// BusinessActor and BusinessAuthority contain recorded facts, never names or
// authority inferred from today's grants. Empty historical labels stay empty.
type BusinessActor struct {
	Kind string  `json:"kind"`
	ID   guid.ID `json:"id,omitempty"`
	Name string  `json:"name"`
}
type BusinessAuthority struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}
type BusinessTarget struct {
	Type  string  `json:"type"`
	ID    guid.ID `json:"id,omitempty"`
	Label string  `json:"label,omitempty"`
}

// BusinessEvent deliberately excludes raw detail, addresses, and user agents.
type BusinessEvent struct {
	AuditEventID      guid.ID           `json:"audit_event_id"`
	Sequence          int64             `json:"sequence"`
	EventType         string            `json:"event_type"`
	Module            string            `json:"module"`
	ProjectID         guid.ID           `json:"project_id,omitempty"`
	Actor             BusinessActor     `json:"actor"`
	Authority         BusinessAuthority `json:"authority"`
	Target            BusinessTarget    `json:"target"`
	Outcome           string            `json:"outcome"`
	OccurredAt        time.Time         `json:"occurred_at"`
	Origin            string            `json:"origin,omitempty"`
	SpawnGrantID      guid.ID           `json:"spawn_grant_id,omitempty"`
	SupervisorAgentID guid.ID           `json:"supervisor_agent_id,omitempty"`
}

type BusinessQuery struct {
	Filter
	EventTypes                          []string
	AuthorityKind, AuthorityRef, Origin string
	SpawnGrantID                        guid.ID
}

// BusinessReader only runs inside the caller's authority-fenced transaction.
// Visibility is evaluated before pagination/counting, including team-level rows.
type BusinessReader func(context.Context, *gorm.DB, BusinessQuery, func(BusinessEvent) (bool, error)) ([]BusinessEvent, int64, error)

func (m *Module) provideBusinessEvents(s *module.Services) error {
	return module.Provide[BusinessReader](s, m.readBusinessOn)
}

func (m *Module) readBusinessOn(ctx context.Context, tx *gorm.DB, in BusinessQuery, visible func(BusinessEvent) (bool, error)) ([]BusinessEvent, int64, error) {
	if m == nil || m.service == nil || m.service.repo == nil {
		return nil, 0, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || visible == nil || authn.TenantIDFromContext(ctx) != in.TenantID {
		return nil, 0, ErrInvalidFilter
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, 0, ErrInvalidFilter
	}
	if err := validateFilter(in.Filter, true); err != nil {
		return nil, 0, err
	}
	if len(in.EventTypes) > 50 || in.SpawnGrantID < 0 || len(in.AuthorityKind) > 32 || len(in.AuthorityRef) > 128 || len(in.Origin) > 32 {
		return nil, 0, ErrInvalidFilter
	}
	q := m.service.repo.tables(tx).Events
	base := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID))
	if !in.From.IsZero() {
		base = base.Where(q.CreatedAt.Gte(in.From))
	}
	if !in.To.IsZero() {
		base = base.Where(q.CreatedAt.Lt(in.To))
	}
	if in.PrincipalID > 0 {
		base = base.Where(q.PrincipalID.Eq(in.PrincipalID))
	}
	if in.TargetID > 0 {
		base = base.Where(q.TargetID.Eq(in.TargetID))
	}
	if in.TargetType != "" {
		base = base.Where(q.TargetType.Eq(in.TargetType))
	}
	if in.Outcome != "" {
		base = base.Where(q.Outcome.Eq(in.Outcome))
	}
	if in.EventType != "" {
		base = base.Where(q.EventType.Eq(in.EventType))
	}
	if len(in.EventTypes) > 0 {
		base = base.Where(q.EventType.In(in.EventTypes...))
	}
	out := []BusinessEvent{}
	var total int64
	// Scan bounded batches in one transaction. Historical JSON facts and the
	// current visibility predicate are applied before counting or slicing.
	for offset := 0; ; offset += 200 {
		rows, err := base.Order(q.CreatedAt.Desc(), q.Sequence.Desc()).Offset(offset).Limit(200).Find()
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			event, ok := businessProjection(eventFromRow(*row))
			if !ok || !businessMatches(event, in) {
				continue
			}
			allowed, err := visible(event)
			if err != nil {
				return nil, 0, err
			}
			if !allowed {
				continue
			}
			if total >= int64(in.Offset) && len(out) < in.Limit {
				out = append(out, event)
			}
			total++
		}
		if len(rows) < 200 {
			return out, total, nil
		}
	}
}

func businessProjection(e Event) (BusinessEvent, bool) {
	var d map[string]json.RawMessage
	if json.Unmarshal(e.Detail, &d) != nil {
		return BusinessEvent{}, false
	}
	text := func(key string) string {
		var s string
		if json.Unmarshal(d[key], &s) == nil {
			return s
		}
		var n json.Number
		if json.Unmarshal(d[key], &n) == nil {
			return n.String()
		}
		return ""
	}
	project, err := guid.OptionalID(text("project_id"))
	// Malformed project facts must not become a team-level visibility bypass.
	if err != nil || (len(d["project_id"]) > 0 && string(d["project_id"]) != "null" && project == 0) {
		return BusinessEvent{}, false
	}
	grant, _ := guid.OptionalID(text("spawn_grant_id"))
	supervisor, _ := guid.OptionalID(text("supervisor_agent_id"))
	kind, ref, _ := strings.Cut(text("authority_source"), ":")
	namespace, _, hasNamespace := strings.Cut(e.EventType, ".")
	if !hasNamespace {
		namespace = ""
	}
	return BusinessEvent{AuditEventID: e.ID, Sequence: e.Sequence, EventType: e.EventType, Module: namespace, ProjectID: project,
		Actor:     BusinessActor{Kind: text("actor_kind"), ID: e.PrincipalID, Name: text("actor_name")},
		Authority: BusinessAuthority{Kind: kind, Ref: ref, Label: text("authority_label")},
		Target:    BusinessTarget{Type: e.TargetType, ID: e.TargetID, Label: text("target_label")},
		Outcome:   e.Outcome, OccurredAt: e.CreatedAt, Origin: text("origin"), SpawnGrantID: grant, SupervisorAgentID: supervisor}, true
}

func businessMatches(e BusinessEvent, q BusinessQuery) bool {
	// SQL text comparisons may be case-insensitive under a deployment's MySQL
	// collation. Recheck exact text before authorization, counts and pagination.
	return (q.EventType == "" || q.EventType == e.EventType) && (len(q.EventTypes) == 0 || slices.Contains(q.EventTypes, e.EventType)) &&
		(q.TargetType == "" || q.TargetType == e.Target.Type) && (q.Outcome == "" || q.Outcome == e.Outcome) &&
		(q.ProjectID == 0 || q.ProjectID == e.ProjectID) && (q.Module == "" || q.Module == e.Module) &&
		(q.ActorKind == "" || q.ActorKind == e.Actor.Kind) &&
		(q.AuthoritySource == "" || q.AuthoritySource == e.Authority.Kind+":"+e.Authority.Ref) &&
		(q.AuthorityKind == "" || q.AuthorityKind == e.Authority.Kind) && (q.AuthorityRef == "" || q.AuthorityRef == e.Authority.Ref) &&
		(q.Origin == "" || q.Origin == e.Origin) && (q.SpawnGrantID == 0 || q.SpawnGrantID == e.SpawnGrantID)
}
