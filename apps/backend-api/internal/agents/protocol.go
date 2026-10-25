package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

var (
	ErrUnavailable = errors.New("agents: unavailable")
	ErrInvalid     = errors.New("agents: invalid input")
	ErrForbidden   = errors.New("agents: forbidden")
	ErrNotFound    = errors.New("agents: not found")
	ErrConflict    = errors.New("agents: conflict")
)

// Inbox contains references only. Source modules remain the authority for
// reading message content, artifacts and execution results.
type AgentInboxItem struct {
	ID        guid.ID   `json:"id"`
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	SubjectID guid.ID   `json:"subject_id"`
	StreamKey string    `json:"stream_key"`
	StreamSeq int64     `json:"stream_seq"`
	CreatedAt time.Time `json:"created_at"`
}
type LiveView struct {
	Runtime          *fleet.AgentRuntimeFact `json:"runtime,omitempty"`
	AgentPrincipalID guid.ID                 `json:"agent_principal_id"`
	Name             string                  `json:"name"`
	IdentityState    string                  `json:"identity_state"`
	Activity         string                  `json:"activity"`
	Head             int64                   `json:"head"`
	Ack              int64                   `json:"ack"`
	SessionEpoch     int                     `json:"session_epoch"`
	WorkspaceEpoch   int                     `json:"workspace_epoch"`
	UpdatedAt        *time.Time              `json:"updated_at,omitempty"`
	Next             []Next                  `json:"next"`
}
type AgentInboxPage struct {
	Knowledge []knowledge.ContextItem `json:"knowledge,omitempty"`
	Items     []AgentInboxItem        `json:"items"`
	Head      int64                   `json:"head"`
	Ack       int64                   `json:"ack"`
	HasMore   bool                    `json:"has_more"`
	Next      []Next                  `json:"next"`
}

// Source modules retain authority over receipt content. These kinds classify
// references only; adding a kind never manufactures a source event.
func validInboxKind(kind string) bool {
	return slices.Contains([]string{"MESSAGE", "MENTION", "REMINDER", "STATE", "EXECUTION", "INTERACTION", "DISPATCH", "REVIEW_FEEDBACK"}, kind)
}

func (m *Module) authorize(ctx context.Context, tx *gorm.DB, tenant, target guid.ID, permission string, humanOnly bool) (fleet.AgentIdentity, error) {
	var empty fleet.AgentIdentity
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || target <= 0 {
		return empty, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrInvalid
	}
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return empty, ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil || m.agents.ByPrincipalOn == nil || m.tenantPolicy == nil {
		return empty, ErrUnavailable
	}
	principal, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return empty, err
	}
	if !active || principal.ID != actor {
		return empty, ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrForbidden
	}
	h, human, err := m.principals.HumanByLogin(ctx, tx, principal.LoginName)
	if err != nil {
		return empty, err
	}
	human = human && h.ID == actor
	if humanOnly && !human {
		return empty, ErrForbidden
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, target)
	if err != nil {
		return empty, err
	}
	if !found || a.TenantID != tenant || a.PrincipalID != target {
		return empty, ErrNotFound
	}
	if permission == "SELF" {
		if human || actor != target || a.State != "ACTIVE" {
			return empty, ErrForbidden
		}
		return a, nil
	}
	// Content-sensitive collaboration views are limited to the human owner or
	// explicit workspace administrators; agent credentials only read SELF views.
	if !human {
		return empty, ErrForbidden
	}
	if permission == "agent_workspace_read" && a.OwnerPrincipalID == actor {
		return a, nil
	}
	c, err := m.tenantPolicy(ctx, tx, tenant, permission, actor)
	if err != nil {
		return empty, err
	}
	if !c.AllowAll && !slices.Contains(c.OwnerIDs, a.OwnerPrincipalID.String()) && !slices.Contains(c.ResourceIDs, a.ID.String()) {
		return empty, ErrForbidden
	}
	return a, nil
}
func (m *Module) lockState(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (*model.LiveState, error) {
	q := query.BindLiveState(tx, m.names)
	// Fleet identity is read before this path; lazy creation does not duplicate
	// the identity, principal, machine, process or enrollment rows.
	if err := q.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model.LiveState{TenantID: tenant, AgentPrincipalID: agent, Activity: "OFFLINE", SessionEpoch: 1, WorkspaceEpoch: 1, UpdatedAt: m.now()}); err != nil {
		return nil, err
	}
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).First()
}
func liveView(a fleet.AgentIdentity, r *model.LiveState) LiveView {
	v := LiveView{AgentPrincipalID: a.PrincipalID, Name: a.Name, IdentityState: a.State, Activity: "OFFLINE", SessionEpoch: 1, WorkspaceEpoch: 1, Next: []Next{{Operation: "inbox_events.list"}}}
	if r != nil {
		v.Activity = r.Activity
		v.Head = r.Head
		v.Ack = r.Ack
		v.SessionEpoch = r.SessionEpoch
		v.WorkspaceEpoch = r.WorkspaceEpoch
		v.UpdatedAt = &r.UpdatedAt
	}
	return v
}
func (m *Module) liveOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (LiveView, error) {
	permission := "agent_workspace_read"
	if authn.PrincipalIDFromContext(ctx) == agent {
		permission = "SELF"
	}
	a, err := m.authorize(ctx, tx, tenant, agent, permission, false)
	if err != nil {
		return LiveView{}, err
	}
	q := query.BindLiveState(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.liveWithRuntime(ctx, tx, tenant, agent, liveView(a, nil))
	}
	if err != nil {
		return LiveView{}, err
	}
	return m.liveWithRuntime(ctx, tx, tenant, agent, liveView(a, r))
}
func (m *Module) inboxOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, since int64, limit int) (AgentInboxPage, error) {
	return m.filteredInboxOn(ctx, tx, tenant, agent, &since, limit, "")
}

// A nil cursor resumes the durable ACK. Explicit zero permits an authorized
// replay from the beginning without changing that ACK.
func (m *Module) filteredInboxOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, since *int64, limit int, kind string) (AgentInboxPage, error) {
	out := AgentInboxPage{Items: []AgentInboxItem{}, Next: []Next{{Operation: "inbox_events.ack"}}}
	if since != nil && *since < 0 || limit < 1 || limit > 100 || kind != "" && !validInboxKind(kind) {
		return out, ErrInvalid
	}
	v, err := m.liveOn(ctx, tx, tenant, agent)
	if err != nil {
		return out, err
	}
	after := v.Ack
	if since != nil {
		after = *since
	}
	if after > v.Head {
		return out, ErrInvalid
	}
	out.Head = v.Head
	out.Ack = v.Ack
	q := query.BindInboxEvent(tx, m.names)
	read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.Seq.Gt(after))
	if kind != "" {
		read = read.Where(q.Kind.Eq(kind))
	}
	rows, err := read.Order(q.Seq).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, r := range rows {
		out.Items = append(out.Items, AgentInboxItem{ID: r.ID, Seq: r.Seq, Kind: r.Kind, SubjectID: r.SubjectID, StreamKey: r.StreamKey, StreamSeq: r.StreamSeq, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
func (m *Module) ackOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, since int64) (LiveView, error) {
	a, err := m.authorize(ctx, tx, tenant, agent, "SELF", false)
	if err != nil {
		return LiveView{}, err
	}
	r, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return LiveView{}, err
	}
	if since < 0 || since > r.Head {
		return LiveView{}, ErrInvalid
	}
	if since <= r.Ack {
		return m.liveWithRuntime(ctx, tx, tenant, agent, liveView(a, r))
	}
	q := query.BindLiveState(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.AgentPrincipalID.Eq(agent), q.TenantID.Eq(tenant), q.Ack.Eq(r.Ack)).Updates(map[string]any{"ack": since, "updated_at": m.now()})
	if err != nil {
		return LiveView{}, err
	}
	if res.RowsAffected != 1 {
		return LiveView{}, ErrConflict
	}
	r.Ack = since
	r.UpdatedAt = m.now()
	if err = m.record(ctx, tx, tenant, agent, "ACK", false); err != nil {
		return LiveView{}, err
	}
	return m.liveWithRuntime(ctx, tx, tenant, agent, liveView(a, r))
}

// InboxAppender is a trusted provider capability: source modules call it in
// their authorized domain transaction. It never accepts message/tool content.
type InboxAppender struct {
	AppendOn func(context.Context, *gorm.DB, guid.ID, guid.ID, InboxReference) (int64, error)
}
type InboxReference struct {
	SourceEventID, Kind, StreamKey string
	SubjectID                      guid.ID
	StreamSeq                      int64
}

func (m *Module) appendOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in InboxReference) (int64, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 || in.SubjectID <= 0 || in.StreamSeq < 0 || len(in.SourceEventID) < 1 || len(in.SourceEventID) > 64 || !validInboxKind(in.Kind) || len(in.StreamKey) > 64 {
		return 0, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalid
	}
	if in.StreamKey != "" {
		kind, id, ok := strings.Cut(in.StreamKey, ":")
		if !ok || !slices.Contains([]string{"channel", "thread", "work_item", "comment_thread"}, kind) {
			return 0, ErrInvalid
		}
		if _, err := guid.RequireID(id); err != nil {
			return 0, ErrInvalid
		}
	}
	if m.agents.ByPrincipalOn == nil || m.ids == nil || m.events == nil {
		return 0, ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return 0, err
	}
	if !found || a.TenantID != tenant || a.PrincipalID != agent {
		return 0, ErrNotFound
	}
	if !a.AcceptsInboxEvents() {
		return 0, ErrForbidden
	}
	r, err := m.lockState(ctx, tx, tenant, agent)
	if err != nil {
		return 0, err
	}
	q := query.BindInboxEvent(tx, m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.SourceEventID.Eq(in.SourceEventID)).First()
	if err == nil {
		return prior.Seq, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	seq := r.Head + 1
	if err = q.WithContext(ctx).Create(&model.InboxEvent{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: agent, Seq: seq, SourceEventID: in.SourceEventID, Kind: in.Kind, SubjectID: in.SubjectID, StreamKey: in.StreamKey, StreamSeq: in.StreamSeq, CreatedAt: m.now()}); err != nil {
		return 0, err
	}
	s := query.BindLiveState(tx, m.names)
	res, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.AgentPrincipalID.Eq(agent), s.Head.Eq(r.Head)).Updates(map[string]any{"head": seq, "updated_at": m.now()})
	if err != nil {
		return 0, err
	}
	if res.RowsAffected != 1 {
		return 0, ErrConflict
	}
	// Wake hints deliberately contain no body, title, subject or tool arguments.
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agents.wake", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:agents.inbox"}, Subject: events.Subject{Kind: "agent", ID: agent}, SubjectVersion: seq, Data: map[string]any{"head": seq}})
	return seq, err
}
func (m *Module) record(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, action string, human bool) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	event := audit.NewDomainEvent(ctx, tenant, "agents.changed", "agent", agent)
	event.Detail["action"] = action
	event.Detail["reason"] = "Agent collaboration protocol command"
	event.Detail["authority_source"] = "SELF:agent"
	event.Detail["evidence_refs"] = []string{}
	kind := "AGENT"
	authority := "SELF:agent"
	if human {
		kind = "HUMAN"
		authority = "IAM:agent_manage"
	}
	event.Detail["authority_source"] = authority
	if err := m.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agents.changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "agent", ID: agent}, SubjectVersion: 1, Data: map[string]any{"action": action}})
	return err
}

func actorID(ctx context.Context) guid.ID  { return authn.PrincipalIDFromContext(ctx) }
func tenantID(ctx context.Context) guid.ID { return authn.TenantIDFromContext(ctx) }

func (m *Module) liveWithRuntime(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, v LiveView) (LiveView, error) {
	if m.runtimeFacts.ByPrincipalOn == nil {
		return v, nil
	}
	f, found, err := m.runtimeFacts.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return LiveView{}, err
	}
	if !found {
		return LiveView{}, ErrNotFound
	}
	v.Runtime = &f
	return v, nil
}
