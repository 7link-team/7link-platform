package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode/utf8"
)

// QualityReviewWindow counts distinct readable work entering Human review from
// immutable transitions, rather than using current work state as a denominator.
type QualityReviewWindow struct {
	WorkIDs []guid.ID
	Entered int
}
type QualityReviewSource func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) (QualityReviewWindow, error)
type QualityReviewConnector struct {
	Bind func(QualityReviewSource) error
}

func (m *Module) bindQualityReviews(source QualityReviewSource) error {
	if source == nil || m.qualityReviews != nil {
		return ErrInvalidArgument
	}
	m.qualityReviews = source
	return nil
}

// PassedGate is a source-owned immutable verification fact, never inferred from
// a human decision or a missing verification receipt.
type PassedGate struct {
	Stage           string  `json:"stage"`
	PrincipalID     guid.ID `json:"principal_id"`
	ModelFamily     string  `json:"model_family"`
	VerifierVersion string  `json:"verifier_version"`
}
type QualityEscapeView struct {
	model.QualityEscape
	PassedBy      []PassedGate                 `json:"passed_by"`
	MemoryIDs     []guid.ID                    `json:"memory_ids"`
	LearningDraft *knowledge.LearningRuleDraft `json:"learning_draft,omitempty"`
}

func escapeView(r *model.QualityEscape) (QualityEscapeView, error) {
	v := QualityEscapeView{QualityEscape: *r, PassedBy: []PassedGate{}, MemoryIDs: []guid.ID{}}
	if json.Unmarshal([]byte(r.PassedBy), &v.PassedBy) != nil || json.Unmarshal([]byte(r.MemoryIDs), &v.MemoryIDs) != nil {
		return v, ErrUnavailable
	}
	if r.LearningDraft != nil {
		var draft knowledge.LearningRuleDraft
		if json.Unmarshal([]byte(*r.LearningDraft), &draft) != nil || draft.Title == "" || draft.Body == "" {
			return v, ErrUnavailable
		}
		v.LearningDraft = &draft
	}
	return v, nil
}
func (m *Module) consumeQualityEscape(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.seed == nil || m.seed.ids == nil || m.publisher == nil {
		return ErrUnavailable
	}
	if e.TenantID <= 0 || e.ProjectID <= 0 || e.EventID <= 0 {
		return ErrInvalidArgument
	}
	var d struct {
		EscapeKey   string       `json:"escape_key"`
		WorkItemID  guid.ID      `json:"work_item_id"`
		Stage       string       `json:"stage"`
		Category    string       `json:"category"`
		Summary     string       `json:"summary"`
		PassedBy    []PassedGate `json:"passed_by"`
		MemoryIDs   []guid.ID    `json:"memory_ids"`
		EvalCaseRef string       `json:"eval_case_ref"`
	}
	if json.Unmarshal(e.Data, &d) != nil || strings.TrimSpace(d.EscapeKey) == "" || len(d.EscapeKey) > 64 {
		return ErrInvalidArgument
	}
	q := query.BindQualityEscapes(tx, m.seed.names)
	old, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(e.TenantID), q.EscapeKey.Eq(d.EscapeKey)).First()
	if e.EventType == "quality.escape_learning_proposed" {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if len(d.EvalCaseRef) > 200 {
			return ErrInvalidArgument
		}
		for _, id := range d.MemoryIDs {
			if id <= 0 {
				return ErrInvalidArgument
			}
		}
		refs, _ := json.Marshal(d.MemoryIDs)
		if d.MemoryIDs == nil {
			refs = []byte("[]")
		}
		// A repeated delivery event cannot replace already frozen draft references.
		if old.MemoryIDs != "[]" || old.EvalCaseRef != nil {
			if old.MemoryIDs != string(refs) || old.EvalCaseRef == nil || *old.EvalCaseRef != d.EvalCaseRef {
				return ErrVersion
			}
			return nil
		}
		_, err = q.WithContext(ctx).Where(q.ID.Eq(old.ID)).Updates(map[string]any{"memory_ids": string(refs), "eval_case_ref": d.EvalCaseRef, "version": old.Version + 1})
		return err
	}
	if e.EventType != "quality.escaped" {
		return ErrInvalidArgument
	}
	if err == nil {
		if old.WorkItemID != d.WorkItemID || old.ProjectID != e.ProjectID {
			return ErrInvalidArgument
		}
		return m.proposeEscapeEvalOn(ctx, tx, old)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if d.WorkItemID <= 0 || (d.Stage != "HUMAN_REVIEW" && d.Stage != "CLIENT_ACCEPTANCE") || strings.TrimSpace(d.Category) == "" || len(d.Category) > 64 || strings.TrimSpace(d.Summary) == "" || utf8.RuneCountInString(d.Summary) > 2000 || len(d.PassedBy) == 0 {
		return ErrInvalidArgument
	}
	for _, g := range d.PassedBy {
		if g.PrincipalID <= 0 || g.Stage == "" || g.ModelFamily == "" || g.VerifierVersion == "" {
			return ErrInvalidArgument
		}
	}
	gates, _ := json.Marshal(d.PassedBy)
	id, err := m.seed.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	r := model.QualityEscape{ID: guid.ID(id), TenantID: e.TenantID, ProjectID: e.ProjectID, WorkItemID: d.WorkItemID, SourceEventID: e.EventID, EscapeKey: d.EscapeKey, Stage: d.Stage, Category: d.Category, Summary: d.Summary, PassedBy: string(gates), MemoryIDs: "[]", LearningState: "PENDING", Version: 1, CreatedAt: m.seed.now()}
	// Generate only a review proposal from immutable source facts. It asserts no
	// diagnosis, corrected implementation, executed regression or approval.
	draft, _ := json.Marshal(knowledge.LearningRuleDraft{
		Title: "Review regression for quality escape " + r.ID.String(),
		Body: "Source quality escape: " + r.ID.String() + "; event: " + r.SourceEventID.String() + "; work item: " + r.WorkItemID.String() + ".\n" +
			"Observed at: " + r.Stage + "; category: " + r.Category + ".\n" +
			"Reported defect: " + r.Summary + "\n" +
			"Review the source evidence and propose a regression reproducing this defect. A Human must review this proposal before it becomes an active rule.",
	})
	encodedDraft := string(draft)
	r.LearningDraft = &encodedDraft
	if err = q.WithContext(ctx).Create(&r); err != nil {
		return err
	}
	if err = m.proposeEscapeEvalOn(ctx, tx, &r); err != nil {
		return err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: "quality.escape_recorded", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:quality.escaped"}, Subject: events.Subject{Kind: "quality_escape", ID: r.ID}, CausationID: e.EventID, Data: map[string]any{"escape_id": r.ID, "work_item_id": r.WorkItemID, "stage": r.Stage}})
	return err
}
func (m *Module) escapeReadAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.delegations.members == nil || m.explain == nil {
		return ErrUnavailable
	}
	if _, active, err := m.principals.LockActiveRow(ctx, tx, actor); err != nil {
		return err
	} else if !active {
		return ErrForbidden
	}
	if active, err := m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, actor); err != nil {
		return err
	} else if !active {
		return ErrForbidden
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: actor, Permission: "audit_read"})
	if err != nil {
		return err
	}
	if !x.Allowed {
		return ErrForbidden
	}
	return nil
}
func (m *Module) escapeDecisionAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if err := m.delegationAuthorityOn(ctx, tx, tenant, project, "policy_manage"); err != nil {
		return err
	}
	if m.delegations.projects.InspectOn == nil {
		return ErrUnavailable
	}
	_, err := m.delegations.projects.InspectOn(ctx, tx, tenant, project)
	return err
}
func (m *Module) decideEscapeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, state, reason string) (QualityEscapeView, error) {
	var out QualityEscapeView
	if (state != "RECORDED" && state != "DISMISSED") || utf8.RuneCountInString(reason) > 2000 || (state == "DISMISSED" && strings.TrimSpace(reason) == "") {
		return out, ErrInvalidArgument
	}
	q := query.BindQualityEscapes(tx, m.seed.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = m.escapeDecisionAuthorityOn(ctx, tx, tenant, r.ProjectID); err != nil {
		return out, err
	}
	r, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return out, err
	}
	if r.Version != version || r.LearningState != "PENDING" {
		return out, ErrVersion
	}
	actor, now := authn.PrincipalIDFromContext(ctx), m.seed.now()
	info, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Version.Eq(version), q.LearningState.Eq("PENDING")).Updates(map[string]any{"learning_state": state, "decision_reason": reason, "decided_by": actor, "decided_at": now, "version": version + 1})
	if err != nil {
		return out, err
	}
	if info.RowsAffected != 1 {
		return out, ErrVersion
	}
	if m.seed.audit == nil || m.publisher == nil {
		return out, ErrUnavailable
	}
	if err = m.seed.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, PrincipalID: actor, EventType: "governance.escape_learning_" + strings.ToLower(state), TargetType: "quality_escape", TargetID: id, Detail: map[string]any{"state": state, "reason": reason, "authority_source": "permission:policy_manage"}}); err != nil {
		return out, err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: r.ProjectID, Type: "quality.escape_learning_decided", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:policy_manage"}, Subject: events.Subject{Kind: "quality_escape", ID: id}, SubjectVersion: int64(version + 1), Data: map[string]any{"escape_id": id, "escape_key": r.EscapeKey, "work_item_id": r.WorkItemID, "state": state, "reason": reason}})
	if err != nil {
		return out, err
	}
	r.LearningState, r.DecisionReason, r.DecidedBy, r.DecidedAt, r.Version = state, &reason, &actor, &now, version+1
	return escapeView(r)
}

// qualityEscapeInbox returns actionable learning decisions, applying current
// Human/project permissions before the limit. Recorded/dismissed rows disappear
// immediately; no stale event projection can retain them in the inbox.
func (m *Module) qualityEscapeInbox(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
	out := []InboxItem{}
	if req.Kind != "QUALITY_ESCAPE" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalidArgument
	}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) || req.PrincipalID <= 0 {
		return nil, ErrForbidden
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.delegations.members == nil {
			return ErrUnavailable
		}
		if err := m.principals.Lock(ctx, tx, req.PrincipalID); err != nil {
			return err
		}
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, req.PrincipalID)
		if err != nil {
			return err
		}
		if !active || human.ID != req.PrincipalID {
			return ErrForbidden
		}
		member, err := m.delegations.members.IsMemberActiveOn(ctx, tx, req.TenantID, req.PrincipalID)
		if err != nil {
			return err
		}
		if !member {
			return ErrForbidden
		}
		q := query.BindQualityEscapes(tx, m.seed.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.LearningState.Eq("PENDING"))
		if req.ProjectID > 0 {
			stmt = stmt.Where(q.ProjectID.Eq(req.ProjectID))
		}
		for offset := 0; ; offset += 100 {
			rows, err := stmt.Order(q.CreatedAt, q.ID).Limit(100).Offset(offset).Find()
			if err != nil {
				return err
			}
			for _, r := range rows {
				if err = m.escapeReadAuthorityOn(ctx, tx, req.TenantID, r.ProjectID); errors.Is(err, ErrForbidden) {
					continue
				} else if err != nil {
					return err
				}
				if err = m.escapeDecisionAuthorityOn(ctx, tx, req.TenantID, r.ProjectID); errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
					continue
				} else if err != nil {
					return err
				}
				item := InboxItem{InboxKey: InboxKey{Priority: "NORMAL", CreatedAt: r.CreatedAt, Kind: req.Kind, ID: r.ID}, ProjectID: r.ProjectID, Title: r.Summary, ObjectType: "quality_escape", ObjectID: r.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
				if req.After != nil && CompareInboxKeys(item.InboxKey, *req.After) <= 0 {
					continue
				}
				out = append(out, item)
				if len(out) == req.Limit {
					return nil
				}
			}
			if len(rows) < 100 {
				return nil
			}
		}
	})
	return out, err
}
