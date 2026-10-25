package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUnavailable            = errors.New("knowledge: unavailable")
	ErrInvalid                = errors.New("knowledge: invalid input")
	ErrForbidden              = errors.New("knowledge: forbidden")
	ErrNotFound               = errors.New("knowledge: not found")
	ErrConflict               = errors.New("knowledge: version or state conflict")
	ErrSelfReview             = errors.Join(ErrForbidden, errors.New("knowledge: writer cannot review own rule"))
	ErrConflictUnacknowledged = errors.Join(ErrInvalid, errors.New("knowledge: rule conflicts require acknowledgement"))
)

type Scope struct {
	Scope            string   `json:"scope" enum:"TEAM,PROJECT,AGENT"`
	ProjectID        *guid.ID `json:"project_id,omitempty"`
	AgentPrincipalID *guid.ID `json:"agent_principal_id,omitempty"`
}
type EntryInput struct {
	edited            bool
	proofAt           time.Time
	inheritedOrigin   string
	riskFlags         []string
	promoted          bool
	SourceExecutionID *guid.ID `json:"source_execution_id,omitempty"`
	TTLDays           int      `json:"ttl_days,omitempty" minimum:"7" maximum:"730"`
	Scope
	Kind       string `json:"kind" enum:"DOCUMENT,EXPERIENCE,RULE"`
	Title      string `json:"title" minLength:"1" maxLength:"200"`
	Body       string `json:"body" minLength:"1" maxLength:"8000"`
	Provenance string `json:"provenance" minLength:"1" maxLength:"512" doc:"Human-provided source description; never treated as verified execution evidence."`
}
type EntryView struct {
	Deduplicated      bool       `json:"deduplicated,omitempty"`
	TTLDays           int        `json:"ttl_days"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	UseCount          int64      `json:"use_count"`
	SourceExecutionID *guid.ID   `json:"source_execution_id,omitempty"`
	SupersedesID      *guid.ID   `json:"supersedes_id,omitempty"`
	RiskFlags         []string   `json:"risk_flags"`
	Origin            string     `json:"origin"`
	ID                guid.ID    `json:"id"`
	Scope
	Kind                     string    `json:"kind"`
	State                    string    `json:"state"`
	Title                    string    `json:"title"`
	Body                     string    `json:"body"`
	Provenance               string    `json:"provenance"`
	ContentHash              string    `json:"content_hash"`
	Version                  int       `json:"version"`
	CreatedBy                guid.ID   `json:"created_by"`
	CreatedAt                time.Time `json:"created_at"`
	ConflictMemoryIDs        []guid.ID `json:"conflict_memory_ids,omitempty"`
	AffectedExecutionIDs     []guid.ID `json:"affected_execution_ids,omitempty" doc:"Historical executions with verified inclusion receipts; revocation prevents future use and does not rewrite frozen execution snapshots."`
	AffectedExecutionsCursor string    `json:"affected_executions_cursor,omitempty"`
}

func entryView(r *model.Entry) EntryView {
	return EntryView{TTLDays: r.TTLDays, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, UseCount: r.UseCount, SourceExecutionID: r.SourceExecutionID, SupersedesID: r.SupersedesID, RiskFlags: memoryFlags(r.RiskFlags), Origin: r.Origin, ID: r.ID, Scope: Scope{Scope: r.Scope, ProjectID: r.ProjectID, AgentPrincipalID: r.AgentPrincipalID}, Kind: r.Kind, State: r.State, Title: r.Title, Body: r.Body, Provenance: r.Provenance, ContentHash: r.ContentHash, Version: r.Version, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
}
func validScope(s Scope) bool {
	if s.ProjectID != nil && *s.ProjectID <= 0 || s.AgentPrincipalID != nil && *s.AgentPrincipalID <= 0 {
		return false
	}
	switch s.Scope {
	case "TEAM":
		return s.ProjectID == nil && s.AgentPrincipalID == nil
	case "PROJECT":
		return s.ProjectID != nil && s.AgentPrincipalID == nil
	case "AGENT":
		return s.AgentPrincipalID != nil
	}
	return false
}

// Scope authorization is performed inside the transaction, before reading
// content, queue membership or counts. Agent memories additionally require the
// owning human or agent_workspace_read; general knowledge_read is insufficient.
func (m *Module) authorize(ctx context.Context, tx *gorm.DB, tenant guid.ID, s Scope, permission string, humanOnly bool) (string, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || !validScope(s) {
		return "", ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return "", ErrInvalid
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return "", ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil || m.policy == nil || m.tenantPolicy == nil {
		return "", ErrUnavailable
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return "", err
	}
	if !active || p.ID != actor {
		return "", ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return "", err
	}
	if !active {
		return "", ErrForbidden
	}
	h, human, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return "", err
	}
	human = human && h.ID == actor
	kind := "HUMAN"
	if !human {
		kind = "AGENT"
		if humanOnly {
			return "", ErrForbidden
		}
		if m.agents.ByPrincipalOn == nil {
			return "", ErrUnavailable
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return "", e
		}
		if !found || a.PrincipalID != actor || a.State != "ACTIVE" {
			return "", ErrForbidden
		}
	}
	var c authz.DataConstraint
	if s.ProjectID != nil {
		c, err = m.policy(ctx, tx, tenant, *s.ProjectID, permission, actor)
	} else {
		c, err = m.tenantPolicy(ctx, tx, tenant, permission, actor)
	}
	if err != nil {
		return "", err
	}
	allowed := c.AllowAll || slices.Contains(c.OwnerIDs, actor.String())
	if s.ProjectID != nil {
		allowed = c.AllowsEntityRows(*s.ProjectID) && (allowed || slices.Contains(c.ResourceIDs, s.ProjectID.String()))
	}
	if !allowed {
		return "", ErrForbidden
	}
	if s.Scope == "AGENT" {
		if m.agents.ByPrincipalOn == nil {
			return "", ErrUnavailable
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, *s.AgentPrincipalID)
		if e != nil {
			return "", e
		}
		if !found || a.PrincipalID != *s.AgentPrincipalID || a.TenantID != tenant {
			return "", ErrNotFound
		}
		if !human {
			if *s.AgentPrincipalID != actor {
				return "", ErrForbidden
			}
		} else if a.OwnerPrincipalID != actor && !(permission == "memory_approve" && memoryApprovalVisible(ctx)) {
			workspace, e := m.tenantPolicy(ctx, tx, tenant, "agent_workspace_read", actor)
			if e != nil {
				return "", e
			}
			if !workspace.AllowAll {
				return "", ErrForbidden
			}
		}
	}
	return kind, nil
}
func (m *Module) writeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in EntryInput) (EntryView, error) {
	var empty EntryView
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	in.Provenance = strings.TrimSpace(in.Provenance)
	if !validScope(in.Scope) || !slices.Contains([]string{"DOCUMENT", "EXPERIENCE", "RULE"}, in.Kind) || in.Title == "" || in.Body == "" || in.Provenance == "" || utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Body) > 8000 || utf8.RuneCountInString(in.Provenance) > 512 {
		return empty, ErrInvalid
	}
	permission, humanOnly := "knowledge_write", false
	if in.promoted {
		permission, humanOnly = "memory_approve", true
	}
	kind, err := m.authorize(ctx, tx, tenant, in.Scope, permission, humanOnly)
	if err != nil {
		return empty, err
	}
	if kind == "AGENT" && (in.Scope.Scope != "AGENT" || in.AgentPrincipalID == nil || *in.AgentPrincipalID != authn.PrincipalIDFromContext(ctx) || in.Kind == "DOCUMENT") {
		return empty, ErrForbidden
	}
	if kind == "HUMAN" && in.Scope.Scope == "AGENT" {
		if _, err = m.authorize(ctx, tx, tenant, Scope{Scope: "AGENT", AgentPrincipalID: in.AgentPrincipalID}, "agent_read", true); err != nil {
			return empty, err
		}
	}
	if kind == "HUMAN" && in.Scope.Scope == "AGENT" && in.ProjectID == nil && in.Kind != "DOCUMENT" {
		if _, err = m.authorize(ctx, tx, tenant, in.Scope, "memory_approve", true); err != nil {
			return empty, err
		}
	}
	if old, found, e := m.existingMemoryOn(ctx, tx, tenant, in); e != nil {
		return empty, e
	} else if found {
		view := entryView(old)
		view.Deduplicated = true
		return view, nil
	}
	if err = m.validateMemoryWriteOn(ctx, tx, tenant, &in, kind); err != nil {
		return empty, err
	}
	if m.ids == nil || m.audit == nil || m.events == nil {
		return empty, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	hash := sha256.Sum256([]byte(in.Title + "\n" + in.Body))
	state := "ACTIVE"
	settings, err := m.settingsValueOn(ctx, tx, tenant)
	if err != nil {
		return empty, err
	}
	if in.Kind == "RULE" || in.Kind == "EXPERIENCE" && settings.MemoryApproval == "ALL_MEMORIES" {
		state = "PENDING_REVIEW"
	}
	if in.promoted {
		state = "ACTIVE"
	}
	now := m.now()
	row := model.Entry{ID: guid.ID(id), TenantID: tenant, Scope: in.Scope.Scope, ProjectID: in.ProjectID, AgentPrincipalID: in.AgentPrincipalID, Kind: in.Kind, State: state, Title: in.Title, Body: in.Body, ContentHash: hex.EncodeToString(hash[:]), Provenance: in.Provenance, Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	m.initializeMemory(&row, in, kind)
	if err = query.BindEntries(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	conflicts, err := m.ruleConflictsOn(ctx, tx, &row)
	if err != nil {
		return empty, err
	}
	if len(conflicts) > 0 {
		flags := memoryFlags(row.RiskFlags)
		flags = append(flags, "CONFLICT")
		encoded, _ := json.Marshal(flags)
		row.RiskFlags = string(encoded)
		q := query.BindEntries(tx, m.names)
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Updates(map[string]any{"risk_flags": row.RiskFlags}); err != nil {
			return empty, err
		}
	}
	if err = m.record(ctx, tx, &row, kind, "WRITTEN", "", row.State, in.Provenance); err != nil {
		return empty, err
	}
	if len(in.riskFlags) > 0 {
		if err = m.record(ctx, tx, &row, kind, "ESCALATED", row.State, row.State, "High-risk memory requires independent Human review"); err != nil {
			return empty, err
		}
	}
	view := entryView(&row)
	view.ConflictMemoryIDs = conflicts
	return view, nil
}

type ReviewInput struct {
	ConflictsAcknowledged bool   `json:"conflicts_acknowledged,omitempty"`
	Version               int    `json:"version" minimum:"1"`
	Action                string `json:"action" enum:"APPROVED,REJECTED,REVOKED"`
	Reason                string `json:"reason" minLength:"5" maxLength:"2000"`
}

func (m *Module) reviewOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in ReviewInput) (EntryView, error) {
	ctx = memoryApprovalContext(ctx)
	var empty EntryView
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Version < 1 || utf8.RuneCountInString(in.Reason) < 5 || utf8.RuneCountInString(in.Reason) > 2000 || !slices.Contains([]string{"APPROVED", "REJECTED", "REVOKED"}, in.Action) {
		return empty, ErrInvalid
	}
	// Fetch only scope facts first. Body is loaded after the current policy fence.
	q := query.BindEntries(tx, m.names)
	scopeRow, err := q.WithContext(ctx).Select(q.ID, q.Scope, q.ProjectID, q.AgentPrincipalID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	permission := "memory_approve"
	if in.Action == "REVOKED" {
		permission = "knowledge_manage"
	}
	actorKind, err := m.authorize(ctx, tx, tenant, Scope{Scope: scopeRow.Scope, ProjectID: scopeRow.ProjectID, AgentPrincipalID: scopeRow.AgentPrincipalID}, permission, true)
	if err != nil {
		return empty, err
	}
	if scopeRow.AgentPrincipalID != nil {
		if _, _, err = m.principals.LockActiveRow(ctx, tx, *scopeRow.AgentPrincipalID); err != nil {
			return empty, err
		}
	}
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return empty, err
	}
	if row.Version != in.Version {
		return empty, ErrConflict
	}
	state := "ACTIVE"
	if in.Action == "REVOKED" {
		state = "REVOKED"
		if row.State != "ACTIVE" {
			return empty, ErrConflict
		}
	} else {
		if (row.Kind != "RULE" && row.Kind != "EXPERIENCE") || row.State != "PENDING_REVIEW" {
			return empty, ErrConflict
		}
		if row.CreatedBy == authn.PrincipalIDFromContext(ctx) {
			return empty, ErrSelfReview
		}
		if in.Action == "REJECTED" {
			state = "REJECTED"
		}
	}
	conflicts, err := m.ruleConflictsOn(ctx, tx, row)
	if err != nil {
		return empty, err
	}
	if in.Action == "APPROVED" && len(conflicts) > 0 && !in.ConflictsAcknowledged {
		return empty, ErrConflictUnacknowledged
	}
	if in.Action == "APPROVED" {
		if err = m.memoryApprovalQuotaOn(ctx, tx, row); err != nil {
			return empty, err
		}
	}
	from := row.State
	row.State = state
	row.Version++
	row.UpdatedAt = m.now()
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(in.Version)).Updates(map[string]any{"state": state, "version": row.Version, "updated_at": row.UpdatedAt, "live_slot": memoryLiveSlot(state)})
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrConflict
	}
	if err = m.record(ctx, tx, row, actorKind, in.Action, from, state, in.Reason, map[string]any{"conflicts_acknowledged": in.ConflictsAcknowledged, "conflict_memory_ids": conflicts}); err != nil {
		return empty, err
	}
	view := entryView(row)
	if in.Action == "REVOKED" {
		page, e := m.affectedExecutionsOn(ctx, tx, tenant, id, "", 50)
		err = e
		view.AffectedExecutionIDs, view.AffectedExecutionsCursor = page.Items, page.NextCursor
	}
	return view, err
}
func (m *Module) record(ctx context.Context, tx *gorm.DB, row *model.Entry, actorKind, action, from, to, reason string, details ...map[string]any) error {
	if m.ids == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actorKind == "SYSTEM" {
		actor = 0
	}
	if err = query.BindReviews(tx, m.names).WithContext(ctx).Create(&model.Review{ID: guid.ID(id), TenantID: row.TenantID, EntryID: row.ID, Action: action, FromState: from, ToState: to, Reason: reason, ActorID: actor, CreatedAt: m.now()}); err != nil {
		return err
	}
	eventType := "knowledge.memory_reviewed"
	if row.Kind != "DOCUMENT" && (row.Scope == "AGENT" || action == "PROMOTED") {
		eventType = "knowledge.memory_" + strings.ToLower(action)
	}
	permission := "memory_approve"
	if action == "WRITTEN" {
		if row.Scope != "AGENT" || row.Kind == "DOCUMENT" {
			eventType = "knowledge.entry_written"
		}
		permission = "knowledge_write"
	} else if action == "REVOKED" || action == "EXPIRED" {
		permission = "knowledge_manage"
	}
	event := audit.NewDomainEvent(ctx, row.TenantID, eventType, "knowledge_entry", row.ID)
	event.Detail["reason"] = reason
	authority := "IAM:" + permission
	if actorKind == "SYSTEM" {
		authority = "SYSTEM:knowledge.memory_forgetting"
	}
	event.Detail["authority_source"] = authority
	event.Detail["evidence_refs"] = []string{}
	event.Detail["action"] = action
	event.Detail["kind"] = row.Kind
	event.Detail["origin"] = row.Origin
	event.Detail["source_execution_id"] = row.SourceExecutionID
	event.Detail["content_hash"] = row.ContentHash
	event.Detail["risk_flags"] = memoryFlags(row.RiskFlags)
	for _, detail := range details {
		for key, value := range detail {
			event.Detail[key] = value
		}
	}
	if err = m.audit(ctx, tx, event); err != nil {
		return err
	}
	project := guid.ID(0)
	if row.ProjectID != nil {
		project = *row.ProjectID
	}
	_, err = m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: project, Type: eventType, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "knowledge_entry", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"state": row.State, "action": action}})
	return err
}
