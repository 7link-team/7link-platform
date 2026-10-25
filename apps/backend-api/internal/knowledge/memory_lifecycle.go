package knowledge

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemoryMutation struct {
	memoryID guid.ID
	Version  int    `json:"version" minimum:"1"`
	Title    string `json:"title" minLength:"1" maxLength:"120"`
	Body     string `json:"body" minLength:"10" maxLength:"1000"`
	TTLDays  int    `json:"ttl_days" minimum:"7" maximum:"730"`
	Reason   string `json:"reason" minLength:"5" maxLength:"2000"`
}

func (m *Module) memoryEntryOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string, human bool) (*model.Entry, string, error) {
	q := query.BindEntries(tx, m.names)
	row, err := q.WithContext(ctx).Select(q.ID, q.Scope, q.ProjectID, q.AgentPrincipalID).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if permission == "memory_approve" {
		ctx = memoryApprovalContext(ctx)
	}
	actor, err := m.authorize(ctx, tx, tenant, Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, permission, human)
	if err != nil {
		return nil, "", err
	}
	if row.AgentPrincipalID != nil {
		if _, _, err = m.principals.LockActiveRow(ctx, tx, *row.AgentPrincipalID); err != nil {
			return nil, "", err
		}
	}
	row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return nil, "", err
	}
	if row.Kind == "DOCUMENT" {
		return nil, "", ErrInvalid
	}
	return row, actor, nil
}
func (m *Module) retireMemoryOn(ctx context.Context, tx *gorm.DB, row *model.Entry, actor, state, reason string) error {
	q := query.BindEntries(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.TenantID.Eq(row.TenantID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": state, "live_slot": nil, "version": row.Version + 1, "updated_at": m.now()})
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	chunks := query.BindChunks(tx, m.names)
	if _, err = chunks.WithContext(ctx).Where(chunks.TenantID.Eq(row.TenantID), chunks.EntryID.Eq(row.ID), chunks.InvalidatedAt.IsNull()).Updates(map[string]any{"invalidated_at": m.now(), "live_slot": nil}); err != nil {
		return err
	}
	before := row.State
	row.State = state
	row.Version++
	return m.record(ctx, tx, row, actor, state, before, state, reason, map[string]any{"lifecycle_action": state})
}
func (m *Module) editMemoryOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in MemoryMutation) (EntryView, error) {
	row, actor, err := m.memoryEntryOn(ctx, tx, tenant, id, "knowledge_write", false)
	if err != nil {
		return EntryView{}, err
	}
	if row.Version != in.Version || row.State != "ACTIVE" || actor == "AGENT" && row.Kind != "EXPERIENCE" {
		return EntryView{}, ErrConflict
	}
	if utf8.RuneCountInString(in.Reason) < 5 || utf8.RuneCountInString(in.Reason) > 2000 {
		return EntryView{}, ErrInvalid
	}
	if err = m.retireMemoryOn(ctx, tx, row, actor, "SUPERSEDED", in.Reason); err != nil {
		return EntryView{}, err
	}
	input := EntryInput{Scope: Scope{Scope: row.Scope, ProjectID: row.ProjectID, AgentPrincipalID: row.AgentPrincipalID}, Kind: row.Kind, Title: in.Title, Body: in.Body, Provenance: row.Provenance, TTLDays: in.TTLDays, SourceExecutionID: row.SourceExecutionID}
	input.edited = true
	if actor == "HUMAN" {
		input.SourceExecutionID = nil
		input.inheritedOrigin = "HUMAN"
	} else {
		input.inheritedOrigin = row.Origin
		input.proofAt = row.CreatedAt
		ancestor := row
		for depth := 0; ancestor.SupersedesID != nil && depth < 1000; depth++ {
			q := query.BindEntries(tx, m.names)
			prior, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*ancestor.SupersedesID)).First()
			if e != nil {
				return EntryView{}, e
			}
			if prior.SourceExecutionID == nil || row.SourceExecutionID == nil || *prior.SourceExecutionID != *row.SourceExecutionID {
				break
			}
			input.proofAt = prior.CreatedAt
			ancestor = prior
		}
	}

	view, err := m.writeOn(ctx, tx, tenant, input)
	if err != nil {
		return EntryView{}, err
	}
	q := query.BindEntries(tx, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(view.ID), q.TenantID.Eq(tenant)).Updates(map[string]any{"supersedes_id": id})
	view.SupersedesID = &id
	if err != nil {
		return EntryView{}, err
	}
	newRow, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(view.ID)).First()
	if e != nil {
		return EntryView{}, e
	}
	err = m.record(ctx, tx, newRow, actor, "EDITED", "", view.State, in.Reason, map[string]any{"supersedes_id": id, "authority_source": "IAM:knowledge_write"})
	return view, err
}

type MemoryPromotion struct {
	Version int    `json:"version" minimum:"1"`
	Target  string `json:"target" enum:"TEAM,AGENT_GLOBAL"`
	Reason  string `json:"reason" minLength:"5" maxLength:"2000"`
}

func (m *Module) promoteMemoryOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in MemoryPromotion) (EntryView, error) {
	row, actor, err := m.memoryEntryOn(ctx, tx, tenant, id, "memory_approve", true)
	if err != nil {
		return EntryView{}, err
	}
	if row.Version != in.Version || row.State != "ACTIVE" || row.Kind != "RULE" || row.Scope != "AGENT" {
		return EntryView{}, ErrConflict
	}
	if utf8.RuneCountInString(in.Reason) < 5 || utf8.RuneCountInString(in.Reason) > 2000 {
		return EntryView{}, ErrInvalid
	}
	scope := Scope{Scope: "TEAM"}
	if in.Target == "AGENT_GLOBAL" {
		if row.ProjectID == nil {
			return EntryView{}, ErrConflict
		}
		scope = Scope{Scope: "AGENT", AgentPrincipalID: row.AgentPrincipalID}
	} else if in.Target != "TEAM" {
		return EntryView{}, ErrInvalid
	}
	if _, err = m.authorize(ctx, tx, tenant, scope, "memory_approve", true); err != nil {
		return EntryView{}, err
	}
	if err = m.retireMemoryOn(ctx, tx, row, actor, "SUPERSEDED", in.Reason); err != nil {
		return EntryView{}, err
	}
	view, err := m.writeOn(ctx, tx, tenant, EntryInput{promoted: true, Scope: scope, Kind: "RULE", Title: row.Title, Body: row.Body, Provenance: row.Provenance})
	if err != nil {
		return EntryView{}, err
	}
	q := query.BindEntries(tx, m.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(view.ID), q.TenantID.Eq(tenant)).Updates(map[string]any{"supersedes_id": id, "origin": row.Origin, "state": "ACTIVE"})
	if err != nil {
		return EntryView{}, err
	}
	view.SupersedesID = &id
	view.Origin = row.Origin
	view.State = "ACTIVE"
	newRow, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(view.ID)).First()
	if e != nil {
		return EntryView{}, e
	}
	if err = m.record(ctx, tx, newRow, actor, "PROMOTED", "", "ACTIVE", in.Reason, map[string]any{"lifecycle_action": "PROMOTED", "supersedes_id": id}); err != nil {
		return EntryView{}, err
	}
	return view, err
}

// This scheduled job changes only memory lifecycle and never invents usage.
func (m *Module) memoryForgetJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return jobs.Permanent(ErrInvalid)
	}
	db, err := m.database()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindEntries(tx, m.names)
		remaining := 1000
		expire := func(row *model.Entry, force bool) error {
			if row.AgentPrincipalID == nil {
				return ErrInvalid
			}
			if _, _, err := m.principals.LockActiveRow(ctx, tx, *row.AgentPrincipalID); err != nil {
				return err
			}
			if row.ProjectID != nil {
				frozen, err := m.archivedProjectOn(ctx, tx, job.TenantID, *row.ProjectID)
				if err != nil || frozen {
					return err
				}
			}
			current, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(job.TenantID), q.ID.Eq(row.ID)).First()
			if err != nil {
				return err
			}
			if current.State != "ACTIVE" || !force && current.ExpiresAt != nil && current.ExpiresAt.After(m.now()) {
				return nil
			}
			systemCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: job.TenantID, SubjectType: authn.SubjectTypePrincipal})
			remaining--
			return m.retireMemoryOn(systemCtx, tx, current, "SYSTEM", "EXPIRED", "EXPERIENCE retention elapsed or live quota exceeded")
		}
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.Scope.Eq("AGENT"), q.Kind.Eq("EXPERIENCE"), q.State.Eq("ACTIVE"), q.ExpiresAt.Lte(m.now())).Order(q.ID).Limit(remaining).Find()
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err = expire(r, false); err != nil {
				return err
			}
		}
		if remaining == 0 {
			return nil
		}
		agents, err := q.WithContext(ctx).Select(q.AgentPrincipalID).Distinct().Where(q.TenantID.Eq(job.TenantID), q.Scope.Eq("AGENT"), q.Kind.Eq("EXPERIENCE"), q.State.Eq("ACTIVE")).Order(q.AgentPrincipalID).Limit(1000).Find()
		if err != nil {
			return err
		}
		for _, agent := range agents {
			if agent.AgentPrincipalID == nil {
				continue
			}
			if _, _, err = m.principals.LockActiveRow(ctx, tx, *agent.AgentPrincipalID); err != nil {
				return err
			}
			base := func() query.IEntryDo {
				return q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.AgentPrincipalID.Eq(*agent.AgentPrincipalID), q.Kind.Eq("EXPERIENCE"), q.State.Eq("ACTIVE"))
			}
			// Candidate counting must not lock entries before their project fence;
			// expire rechecks and locks each selected entry under the owner lock.
			live, e := base().Select(q.ID).Limit(1501).Find()
			count := len(live)
			if e != nil {
				return e
			}
			excess := count - 500
			for pass := 0; pass < 2 && excess > 0 && remaining > 0; pass++ {
				read := base()
				if pass == 0 {
					read = read.Where(q.LastUsedAt.IsNull())
				} else {
					read = read.Where(q.LastUsedAt.IsNotNull())
				}
				limit := min(excess, remaining)
				victims, e := read.Order(q.CreatedAt, q.ID).Limit(limit).Find()
				if e != nil {
					return e
				}
				for _, r := range victims {
					if err = expire(r, true); err != nil {
						return err
					}
					excess--
				}
			}
			if remaining == 0 {
				break
			}
		}
		return nil
	})
}

// memoryUsageOn is called only after a new verified inclusion receipt was inserted.
func (m *Module) memoryUsageOn(ctx context.Context, tx *gorm.DB, entry *model.Entry) error {
	if entry.Kind == "DOCUMENT" {
		return nil
	}
	now := m.now()
	updates := map[string]any{"use_count": entry.UseCount + 1}
	effectiveUse := now
	if entry.LastUsedAt == nil || !entry.LastUsedAt.UTC().Truncate(24*time.Hour).Equal(now.UTC().Truncate(24*time.Hour)) {
		updates["last_used_at"] = now
	} else {
		effectiveUse = *entry.LastUsedAt
	}
	if entry.Kind == "EXPERIENCE" {
		ttl := entry.TTLDays
		if ttl == 0 {
			ttl = 180
		}
		updates["expires_at"] = effectiveUse.AddDate(0, 0, ttl)
	}
	q := query.BindEntries(tx, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(entry.ID), q.TenantID.Eq(entry.TenantID)).Updates(updates)
	return err
}
