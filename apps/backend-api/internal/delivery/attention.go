package delivery

import (
	di "7link.com/platform/internal/delivery/i18n"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

const attentionJob = "delivery.attention_close"
const attentionPath = workPath + "/{work_item_id}/attention-sessions"

var ErrAttentionClosed = errors.New("delivery: attention session closed")
var ErrAttentionSequence = errors.New("delivery: attention sequence gap")

type attentionCreateBody struct {
	ClaimID       guid.ID `json:"claim_id,omitempty"`
	InteractionID guid.ID `json:"interaction_id,omitempty"`
	Surface       string  `json:"surface" enum:"REVIEW_DESK,INBOX"`
}
type attentionBeatBody struct {
	Sequence      int64 `json:"sequence" minimum:"1"`
	ActiveMSDelta int64 `json:"active_ms_delta" minimum:"0"`
	Ended         bool  `json:"ended"`
}
type attentionCreateInput struct {
	ResultWorkInput
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           attentionCreateBody
}
type attentionInput struct {
	ResultWorkInput
	ID   string `path:"attention_session_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body attentionBeatBody
}
type attentionView struct {
	ID                     guid.ID    `json:"id"`
	ActiveMS               int64      `json:"active_ms"`
	LastSequence           int64      `json:"last_sequence"`
	EndedAt                *time.Time `json:"ended_at"`
	ReviewerLeaseExpiresAt *time.Time `json:"reviewer_lease_expires_at"`
	HeartbeatIntervalMS    int        `json:"heartbeat_interval_ms"`
	IdleCutoffMS           int        `json:"idle_cutoff_ms"`
}
type attentionTotal struct {
	ActiveMS     int64 `json:"active_ms"`
	SessionCount int64 `json:"session_count"`
	Measured     bool  `json:"measured"`
}

func attentionError(ctx context.Context, err error) error {
	if errors.Is(err, ErrAttentionClosed) {
		return respx.ErrStatus(ctx, 409, 120454, di.AttentionClosed, nil)
	}
	if errors.Is(err, ErrAttentionSequence) {
		return respx.ErrStatus(ctx, 422, 120454, di.AttentionClosed, nil)
	}
	if errors.Is(err, ErrInvalidMember) {
		return respx.ErrStatus(ctx, 403, 120403, di.HumanOnly, nil)
	}
	return resultHTTPError(ctx, err)
}
func (m *Module) registerAttentionHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.workOwner)
	guard := authz.Guard{Resource: "work_item", Verb: "read"}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "attention_session.create", Method: "POST", Path: attentionPath, Summary: "Start a Human review attention session", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, "work_item_id", owner, guard, m.createAttentionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "attention_session.heartbeat", Method: "PUT", Path: attentionPath + "/{attention_session_id}", Summary: "Accumulate bounded visible active Human review time", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, "work_item_id", owner, guard, m.heartbeatAttentionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "attention_session.total", Method: "GET", Path: attentionPath, Summary: "Read measured work attention including undecided sessions", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 422, 500, 503}}, "work_item_id", owner, guard, m.totalAttentionHTTP)
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, attentionPath)
}
func (m *Module) attentionWorkOn(ctx context.Context, tx *gorm.DB, p, work guid.ID) (*model.WorkItem, error) {
	if m == nil || m.service == nil || tx == nil {
		return nil, ErrUnavailable
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	// Discover only the principal lock set. The subsequent locking work read
	// verifies this owner again; this snapshot grants no authority.
	w := query.BindWorkItems(tx, m.service.names)
	found, err := w.WithContext(ctx).Select(w.AccountablePrincipalID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(p), w.ID.Eq(work), w.ArchivedAt.IsNull()).Take()
	if err != nil {
		return nil, err
	}
	_, row, _, err := m.lockedWorkOn(ctx, tx, tenant, p, work, actor, found.AccountablePrincipalID, "work_item_read")
	return row, err
}
func attentionRecord(row *model.AttentionSession) *attentionView {
	return &attentionView{ID: row.ID, ActiveMS: row.ActiveMS, LastSequence: row.LastSequence, EndedAt: row.EndedAt, HeartbeatIntervalMS: 15000, IdleCutoffMS: 60000}
}
func (m *Module) attentionEventOn(ctx context.Context, tx *gorm.DB, row *model.AttentionSession, kind string, system bool) error {
	actor := events.Actor{Kind: "HUMAN", ID: row.PrincipalID, AuthoritySource: "permission:work_item_read"}
	if system {
		actor = events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:attention_close"}
	}
	e := audit.NewDomainEvent(ctx, row.TenantID, kind, Name, row.ID)
	e.Detail["authority_source"] = actor.AuthoritySource
	e.Detail["actor_kind"] = actor.Kind
	e.Detail["work_item_id"] = row.WorkItemID.String()
	e.Detail["active_ms"] = row.ActiveMS
	if system {
		e.PrincipalID = 0
	}
	e.Detail["decision_id"] = row.DecisionID
	if err := m.service.audit(ctx, tx, e); err != nil {
		return err
	}
	_, err := m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: kind, Actor: actor, Subject: events.Subject{Kind: "work_item", ID: row.WorkItemID}, SubjectVersion: row.LastSequence + 1, Data: map[string]any{"decision_id": row.DecisionID, "attention_session_id": row.ID.String(), "claim_id": row.ClaimID, "interaction_id": row.InteractionID, "active_ms": row.ActiveMS, "sequence": row.LastSequence}})
	return err
}
func (m *Module) createAttentionOn(ctx context.Context, tx *gorm.DB, p, work guid.ID, in attentionCreateBody, at time.Time) (attentionView, error) {
	row, err := m.attentionWorkOn(ctx, tx, p, work)
	if err != nil {
		return attentionView{}, err
	}
	if in.Surface != "REVIEW_DESK" && in.Surface != "INBOX" {
		return attentionView{}, ErrInvalidArgument
	}
	var claim, interaction *guid.ID
	if in.InteractionID > 0 {
		if in.Surface != "INBOX" || in.ClaimID != 0 {
			return attentionView{}, ErrInvalidArgument
		}
		if m.attentionInteraction == nil {
			return attentionView{}, ErrUnavailable
		}
		if err := m.attentionInteraction(ctx, tx, row.TenantID, p, work, in.InteractionID); err != nil {
			return attentionView{}, err
		}
		interaction = &in.InteractionID
	} else {
		if in.ClaimID <= 0 {
			return attentionView{}, ErrInvalidArgument
		}
		if row.State != "NEEDS_HUMAN_REVIEW" || row.CurrentClaimID == nil || *row.CurrentClaimID != in.ClaimID {
			return attentionView{}, ErrResultState
		}
		claim = &in.ClaimID
	}
	id, err := m.ids.Next()
	if err != nil {
		return attentionView{}, err
	}
	session := &model.AttentionSession{TenantID: row.TenantID, ID: guid.ID(id), ProjectID: p, WorkItemID: work, ClaimID: claim, InteractionID: interaction, PrincipalID: authn.PrincipalIDFromContext(ctx), Surface: in.Surface, StartedAt: at, LastHeartbeatAt: at}
	if err = query.BindAttentionSessions(tx, m.service.names).WithContext(ctx).Create(session); err != nil {
		return attentionView{}, err
	}
	err = m.attentionEventOn(ctx, tx, session, "work_item.attention_started", false)
	return *attentionRecord(session), err
}
func (m *Module) createAttentionHTTP(ctx context.Context, in *attentionCreateInput) (*deliveryCommandOutput[attentionView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	return deliveryCommand(ctx, m, "work_item", attentionError, "attention_session.create", "POST", attentionPath, in.IdempotencyKey, 201, in.Body, map[string]string{"entity_id": p.String(), "work_item_id": work.String()}, func(tx *gorm.DB) (attentionView, guid.ID, error) {
		v, e := m.createAttentionOn(ctx, tx, p, work, in.Body, time.Now().UTC().Truncate(time.Millisecond))
		return v, v.ID, e
	}, func(tx *gorm.DB) error { _, e := m.attentionWorkOn(ctx, tx, p, work); return e })
}
func (m *Module) heartbeatAttentionOn(ctx context.Context, tx *gorm.DB, p, work, id guid.ID, in attentionBeatBody, at time.Time) (attentionView, error) {
	w, err := m.attentionWorkOn(ctx, tx, p, work)
	if err != nil {
		return attentionView{}, err
	}
	q := query.BindAttentionSessions(tx, m.service.names)
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(w.TenantID), q.ProjectID.Eq(p), q.WorkItemID.Eq(work), q.ID.Eq(id), q.PrincipalID.Eq(authn.PrincipalIDFromContext(ctx))).Take()
	if err != nil {
		return attentionView{}, err
	}
	if in.Sequence < 1 || in.ActiveMSDelta < 0 {
		return attentionView{}, ErrInvalidArgument
	}
	// Exact and older sequence retries never add time or renew a lease twice.
	if in.Sequence <= row.LastSequence {
		return *attentionRecord(row), nil
	}
	if row.EndedAt != nil || at.Sub(row.LastHeartbeatAt) > 45*time.Second || at.Sub(row.StartedAt) >= 4*time.Hour {
		return attentionView{}, ErrAttentionClosed
	}
	if row.InteractionID != nil {
		if m.attentionInteraction == nil {
			return attentionView{}, ErrUnavailable
		}
		if err := m.attentionInteraction(ctx, tx, w.TenantID, p, work, *row.InteractionID); err != nil {
			return attentionView{}, err
		}
	} else if row.ClaimID == nil || w.State != "NEEDS_HUMAN_REVIEW" || w.CurrentClaimID == nil || *w.CurrentClaimID != *row.ClaimID {
		return attentionView{}, ErrAttentionClosed
	}
	if in.Sequence != row.LastSequence+1 {
		return attentionView{}, ErrAttentionSequence
	}
	delta := min(in.ActiveMSDelta, int64(17000), max(int64(0), at.Sub(row.LastHeartbeatAt).Milliseconds()+2000), int64(14400000)-row.ActiveMS)
	row.ActiveMS += delta
	row.LastSequence = in.Sequence
	row.LastHeartbeatAt = at
	if in.Ended {
		row.EndedAt = &at
	}
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(id)).Select(q.ActiveMS, q.LastSequence, q.LastHeartbeatAt, q.EndedAt).Updates(row); err != nil {
		return attentionView{}, err
	}
	out := attentionRecord(row)
	a := query.BindAssignments(tx, m.service.names)
	reviewers, err := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(row.TenantID), a.ProjectID.Eq(p), a.WorkItemID.Eq(work), a.AssigneePrincipalID.Eq(row.PrincipalID), a.AssigneeKind.Eq("HUMAN"), a.Role.Eq("REVIEWER"), a.Status.Eq("ACTIVE")).Find()
	if err != nil {
		return attentionView{}, err
	}
	for _, r := range reviewers {
		if r.LeaseExpiresAt != nil && r.LeaseExpiresAt.After(at) {
			expires := at.Add(time.Duration(r.LeaseTTLMS) * time.Millisecond)
			if _, err = a.WithContext(ctx).Where(a.TenantID.Eq(r.TenantID), a.ID.Eq(r.ID)).UpdateSimple(a.LeaseExpiresAt.Value(expires), a.LeaseRenewedAt.Value(at), a.UpdatedAt.Value(at), a.Version.Add(1)); err != nil {
				return attentionView{}, err
			}
			out.ReviewerLeaseExpiresAt = &expires
		}
	}
	err = m.attentionEventOn(ctx, tx, row, "work_item.attention_updated", false)
	return *out, err
}
func (m *Module) heartbeatAttentionHTTP(ctx context.Context, in *attentionInput) (*respx.Body[attentionView], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.ID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, attentionError(ctx, ErrUnavailable)
	}
	var out attentionView
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.heartbeatAttentionOn(ctx, tx, p, work, id, in.Body, time.Now().UTC().Truncate(time.Millisecond))
		return err
	})
	if e != nil {
		return nil, attentionError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) totalAttentionHTTP(ctx context.Context, in *ResultWorkInput) (*respx.Body[attentionTotal], error) {
	p, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, attentionError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, attentionError(ctx, ErrUnavailable)
	}
	var out attentionTotal
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
		if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
			return ErrUnavailable
		}
		if tenant <= 0 || actor <= 0 {
			return ErrInvalidMember
		}
		if e := m.principals.Lock(ctx, tx, actor); e != nil {
			return e
		}
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
		if e != nil {
			return e
		}
		if !active || human.ID != actor {
			return ErrInvalidMember
		}
		active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
		if e != nil {
			return e
		}
		if !active {
			return ErrInvalidMember
		}
		// Historical totals use the work's current read/owner constraint; session
		// creation and heartbeats keep the active-project mutation fence.
		_, e = m.readWork(ctx, tx, tenant, p, work, actor)
		if e != nil {
			return e
		}
		q := query.BindAttentionSessions(tx, m.service.names)
		return q.WithContext(ctx).Select(q.ActiveMS.Sum().IfNull(0).As("active_ms"), q.ID.Count().As("session_count")).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(p), q.WorkItemID.Eq(work)).Scan(&out)
	})
	if e != nil {
		return nil, attentionError(ctx, e)
	}
	out.Measured = out.SessionCount > 0
	return respx.OK(ctx, out), nil
}

// Call under the already-held work fence, before inserting an immutable decision.
func (m *Module) closeDecisionAttentionOn(ctx context.Context, tx *gorm.DB, action *model.ResultAction, at time.Time) error {
	if action.ActorID <= 0 {
		return nil
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, action.ActorID)
	if err != nil {
		return err
	}
	if !active || human.ID != action.ActorID {
		return nil
	}
	q := query.BindAttentionSessions(tx, m.service.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(action.TenantID), q.ProjectID.Eq(action.ProjectID), q.WorkItemID.Eq(action.WorkItemID), q.ClaimID.Eq(action.ResultID), q.PrincipalID.Eq(action.ActorID)).Find()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	var total int64
	for _, r := range rows {
		total += r.ActiveMS
		if r.DecisionID == nil {
			r.DecisionID = &action.ID
			if r.EndedAt == nil {
				r.EndedAt = &at
			}
			if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID)).Select(q.DecisionID, q.EndedAt).Updates(r); err != nil {
				return err
			}
		}
	}
	action.AttentionMS = &total
	measured := *rows[0]
	measured.ActiveMS = total
	measured.DecisionID = &action.ID
	return m.attentionEventOn(ctx, tx, &measured, "work_item.attention_decided", false)
}
func (m *Module) attentionCloseHandler() jobs.Handler {
	return jobs.Handler{DisplayNameKey: di.Namespace + ".activity_job_delivery_attention_close_name", DescriptionKey: di.Namespace + ".activity_job_delivery_attention_close_description", Kind: attentionJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		return m.closeStaleAttention(ctx, job.TenantID, time.Now().UTC().Truncate(time.Millisecond))
	}}
}
func (m *Module) closeStaleAttention(ctx context.Context, tenant guid.ID, at time.Time) error {
	if m == nil || m.service == nil || m.principals.Lock == nil || m.service.audit == nil || m.service.events == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return ErrUnavailable
	}
	db := m.runtime.Database().Write()
	q := query.BindAttentionSessions(db, m.service.names)
	stale := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.EndedAt.IsNull()).Where(q.LastHeartbeatAt.Lt(at.Add(-45 * time.Second))).Or(q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.EndedAt.IsNull(), q.StartedAt.Lte(at.Add(-4*time.Hour))))
	rows, err := stale.Order(q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.principals.Lock(ctx, tx, r.PrincipalID); err != nil {
				return err
			}
			p := query.BindProjects(tx, m.service.names)
			if _, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(r.ProjectID)).Take(); err != nil {
				return err
			}
			s := query.BindAttentionSessions(tx, m.service.names)
			current, err := s.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(s.TenantID.Eq(tenant), s.ID.Eq(r.ID)).Take()
			if err != nil {
				return err
			}
			if current.EndedAt != nil || (at.Sub(current.LastHeartbeatAt) <= 45*time.Second && at.Sub(current.StartedAt) < 4*time.Hour) {
				return nil
			}
			end := minTime(current.LastHeartbeatAt.Add(45*time.Second), current.StartedAt.Add(4*time.Hour))
			current.EndedAt = &end
			if _, err = s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.ID.Eq(r.ID)).UpdateSimple(s.EndedAt.Value(end)); err != nil {
				return err
			}
			return m.attentionEventOn(ctx, tx, current, "work_item.attention_closed", true)
		}); err != nil {
			return err
		}
	}
	return nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
