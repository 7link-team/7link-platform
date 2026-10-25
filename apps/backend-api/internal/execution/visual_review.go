package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// VisualReview binds a recorded judgement to immutable platform screenshot facts.
// VISUAL_REVIEW is reserved for this command and excluded from runner evidence.
type VisualReview struct {
	TaskID             guid.ID `json:"task_id"`
	PlanHash           string  `json:"plan_hash" minLength:"64" maxLength:"64"`
	CaptureReceiptHash string  `json:"capture_receipt_hash" minLength:"64" maxLength:"64"`
	Verdict            string  `json:"verdict" enum:"PASSED,FAILED"`
	Reason             string  `json:"reason" minLength:"1" maxLength:"2000"`
}

type VisualReviewDecision struct {
	VisualReview
	ReviewerID    guid.ID   `json:"reviewer_id"`
	ReviewerKind  string    `json:"reviewer_kind"`
	Model         string    `json:"model,omitempty"`
	Family        string    `json:"family,omitempty"`
	HeadCommit    string    `json:"head_commit"`
	ResultID      guid.ID   `json:"result_id"`
	ResultVersion int       `json:"result_version"`
	CriterionID   guid.ID   `json:"criterion_id"`
	DecidedAt     time.Time `json:"decided_at"`
}

func visualReviewTitle(id guid.ID) string { return "Visual review " + id.String() }

// Agent image reads are restricted to platform VISUAL screenshots. Ordinary
// executor artifacts and logs retain their existing Human-only read boundary.
func (m *Module) authorizeVisualReviewerReadOn(ctx context.Context, tx *gorm.DB, row *model.Execution, evidenceID guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != row.TenantID || m.agents.ByPrincipalOn == nil || m.policy == nil {
		return ErrForbidden
	}
	v, err := view(row)
	if err != nil {
		return err
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, row.TenantID, actor)
	if err != nil {
		return err
	}
	if !found || a.TenantID != row.TenantID || a.PrincipalID != actor || a.State != "ACTIVE" || (!slices.Contains(a.Roles, "REVIEWER") && !slices.Contains(a.Roles, "ACCEPTOR")) || actor == row.InitiatorID || actor == v.Plan.ExecutorPrincipalID || v.Plan.ModelFamily == "" || a.ModelFamily == "" || a.ModelFamily == v.Plan.ModelFamily || a.ModelID == "" || a.ModelID == v.Plan.Model {
		return ErrForbidden
	}
	for _, permission := range []string{"execution_read", "verification_run"} {
		c, e := m.policy(ctx, tx, row.TenantID, row.ProjectID, permission, actor)
		if e != nil {
			return e
		}
		if !c.AllowsEntityRows(row.ProjectID) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, row.ProjectID.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
			return ErrForbidden
		}
	}
	e := query.BindEvidence(tx, m.names)
	file, err := e.WithContext(ctx).Where(e.TenantID.Eq(row.TenantID), e.ExecutionID.Eq(row.ID), e.ID.Eq(evidenceID), e.Kind.Eq("FILE")).First()
	if err != nil {
		return ErrForbidden
	}
	parts := strings.Fields(file.Title)
	if len(parts) != 4 || parts[0] != "Visual" || parts[1] != "verification" {
		return ErrForbidden
	}
	taskID, err := guid.RequireID(parts[2])
	if err != nil {
		return ErrForbidden
	}
	q := query.BindVerificationTasks(tx, m.names)
	task, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ExecutionID.Eq(row.ID), q.ID.Eq(taskID), q.State.Eq("DONE")).First()
	if err != nil {
		return ErrForbidden
	}
	var p VerificationPlan
	var o VerificationObservation
	if hash([]byte(task.PlanJSON)) != task.PlanHash || hash([]byte(task.ObservationJSON)) != task.ReceiptHash || json.Unmarshal([]byte(task.PlanJSON), &p) != nil || json.Unmarshal([]byte(task.ObservationJSON), &o) != nil || p.Method != "VISUAL" {
		return ErrForbidden
	}
	for _, screenshot := range o.Screenshots {
		if screenshot.SHA256 == file.ContentHash {
			return nil
		}
	}
	return ErrForbidden
}

type VisualReviewCapture struct {
	TaskID             guid.ID               `json:"task_id"`
	PlanHash           string                `json:"plan_hash"`
	CaptureReceiptHash string                `json:"capture_receipt_hash"`
	HeadCommit         string                `json:"head_commit"`
	ResultID           guid.ID               `json:"result_id"`
	ResultVersion      int                   `json:"result_version"`
	CriterionID        guid.ID               `json:"criterion_id"`
	BrowserProvider    string                `json:"browser_provider"`
	CaptureReason      string                `json:"capture_reason"`
	Unverified         []string              `json:"unverified"`
	Screenshots        []VisualScreenshot    `json:"screenshots"`
	Evidence           []EvidenceFact        `json:"evidence"`
	Decision           *VisualReviewDecision `json:"decision,omitempty"`
}

type VisualReviewsPage struct {
	Items      []VisualReviewCapture `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

func (m *Module) listVisualReviewsOn(ctx context.Context, tx *gorm.DB, tenant, execution, cursor guid.ID, limit int) (VisualReviewsPage, error) {
	out := VisualReviewsPage{Items: []VisualReviewCapture{}}
	if transaction(tx, tenant, execution) != nil || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	r, err := m.rowOn(ctx, tx, tenant, execution, false)
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrForbidden
	}
	if m.policy == nil || m.principals.ActiveHumanByID == nil {
		return out, ErrUnavailable
	}
	if err = m.principalLocks(ctx, tx, actor); err != nil {
		return out, err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return out, err
	}
	if !active {
		if m.agents.ByPrincipalOn == nil {
			return out, ErrForbidden
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return out, e
		}
		if !found || a.TenantID != tenant || a.PrincipalID != actor || a.State != "ACTIVE" {
			return out, ErrForbidden
		}
	}
	c, err := m.policy(ctx, tx, tenant, r.ProjectID, "execution_read", actor)
	if err != nil {
		return out, err
	}
	if !c.AllowsEntityRows(r.ProjectID) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, r.ProjectID.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return out, ErrForbidden
	}
	q := query.BindVerificationTasks(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution), q.State.Eq("DONE"), q.ID.Gt(cursor)).Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.HasMore = true
		out.NextCursor = rows[limit-1].ID.String()
	}
	for _, task := range rows {
		var p VerificationPlan
		var o VerificationObservation
		if hash([]byte(task.PlanJSON)) != task.PlanHash || hash([]byte(task.ObservationJSON)) != task.ReceiptHash || json.Unmarshal([]byte(task.PlanJSON), &p) != nil || json.Unmarshal([]byte(task.ObservationJSON), &o) != nil {
			return out, ErrUnavailable
		}
		if p.Method != "VISUAL" {
			continue
		}
		capture := VisualReviewCapture{TaskID: task.ID, PlanHash: task.PlanHash, CaptureReceiptHash: task.ReceiptHash, HeadCommit: p.HeadCommit, ResultID: task.ResultID, ResultVersion: task.ResultVersion, CriterionID: task.CriterionID, BrowserProvider: o.BrowserProvider, CaptureReason: o.Reason, Unverified: o.Unverified, Screenshots: storedVerificationObservation(o).Screenshots, Evidence: []EvidenceFact{}}
		for _, screenshot := range o.Screenshots {
			e := query.BindEvidence(tx, m.names)
			file, err := e.WithContext(ctx).Where(e.TenantID.Eq(tenant), e.ExecutionID.Eq(execution), e.Kind.Eq("FILE"), e.ContentHash.Eq(screenshot.SHA256)).First()
			if err != nil {
				return out, err
			}
			var artifact ArtifactFact
			if json.Unmarshal([]byte(file.Content), &artifact) != nil {
				return out, ErrUnavailable
			}
			capture.Evidence = append(capture.Evidence, EvidenceFact{ID: file.ID, Kind: file.Kind, Title: file.Title, ContentHash: file.ContentHash, Artifact: &artifact})
		}
		capture.Decision, _, err = m.visualReviewOn(ctx, tx, task)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, capture)
	}
	return out, nil
}

func (m *Module) visualReviewOn(ctx context.Context, tx *gorm.DB, r *model.VerificationTask) (*VisualReviewDecision, string, error) {
	q := query.BindEvidence(tx, m.names)
	e, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ProjectID.Eq(r.ProjectID), q.ExecutionID.Eq(r.ExecutionID), q.Kind.Eq("VISUAL_REVIEW"), q.Title.Eq(visualReviewTitle(r.ID))).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var d VisualReviewDecision
	if hash([]byte(e.Content)) != e.ContentHash || json.Unmarshal([]byte(e.Content), &d) != nil || d.TaskID != r.ID || d.PlanHash != r.PlanHash || d.CaptureReceiptHash != r.ReceiptHash || d.ResultID != r.ResultID || d.ResultVersion != r.ResultVersion || d.CriterionID != r.CriterionID || d.ReviewerID <= 0 || !slices.Contains([]string{"HUMAN", "AGENT"}, d.ReviewerKind) || !slices.Contains([]string{"PASSED", "FAILED"}, d.Verdict) || !bounded(strings.TrimSpace(d.Reason), 2000) {
		return nil, "", ErrUnavailable
	}
	return &d, e.ContentHash, nil
}

// RecordVisualReviewOn checks current authority even for identical retries. The
// task lock serializes first decision; evidence, audit and outbox share this tx.
func (m *Module) RecordVisualReviewOn(ctx context.Context, tx *gorm.DB, tenant, execution guid.ID, in VisualReview) (VisualReviewDecision, error) {
	var empty VisualReviewDecision
	if transaction(tx, tenant, execution) != nil || in.TaskID <= 0 || !slices.Contains([]string{"PASSED", "FAILED"}, in.Verdict) || !bounded(strings.TrimSpace(in.Reason), 2000) {
		return empty, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return empty, ErrForbidden
	}
	snap, err := m.rowOn(ctx, tx, tenant, execution, false)
	if err != nil {
		return empty, err
	}
	v, err := view(snap)
	if err != nil {
		return empty, err
	}
	participants := []guid.ID{actor}
	for _, id := range []guid.ID{snap.InitiatorID, v.Plan.ExecutorPrincipalID} {
		if id > 0 {
			participants = append(participants, id)
		}
	}
	if err = m.workPrincipalLocks(ctx, tx, link(snap, actor), participants...); err != nil {
		return empty, err
	}
	if actor == v.Plan.ExecutorPrincipalID || actor == snap.InitiatorID {
		return empty, ErrForbidden
	}
	if m.principals.ActiveHumanByID == nil || m.policy == nil || m.work.AuthorizeVisualReviewOn == nil {
		return empty, ErrUnavailable
	}
	_, human, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return empty, err
	}
	d := VisualReviewDecision{VisualReview: in, ReviewerID: actor, ReviewerKind: "HUMAN"}
	if !human {
		if m.agents.ByPrincipalOn == nil {
			return empty, ErrUnavailable
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return empty, e
		}
		if !found || a.TenantID != tenant || a.PrincipalID != actor || a.State != "ACTIVE" || (!slices.Contains(a.Roles, "ACCEPTOR") && !slices.Contains(a.Roles, "REVIEWER")) || v.Plan.ExecutorPrincipalID <= 0 || v.Plan.ModelFamily == "" || a.ModelFamily == "" || a.ModelFamily == v.Plan.ModelFamily || a.ModelID == "" || a.ModelID == v.Plan.Model {
			return empty, ErrForbidden
		}
		d.ReviewerKind, d.Model, d.Family = "AGENT", a.ModelID, a.ModelFamily
	}
	c, err := m.policy(ctx, tx, tenant, snap.ProjectID, "verification_run", actor)
	if err != nil {
		return empty, err
	}
	if !c.AllowsEntityRows(snap.ProjectID) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, snap.ProjectID.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return empty, ErrForbidden
	}
	q := query.BindVerificationTasks(tx, m.names)
	taskSnapshot, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution), q.ID.Eq(in.TaskID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	var pinned VerificationPlan
	if hash([]byte(taskSnapshot.PlanJSON)) != taskSnapshot.PlanHash || json.Unmarshal([]byte(taskSnapshot.PlanJSON), &pinned) != nil {
		return empty, ErrUnavailable
	}
	if pinned.ID != taskSnapshot.ID.String() || pinned.ResultID != taskSnapshot.ResultID.String() || pinned.ResultVersion != taskSnapshot.ResultVersion || pinned.CriterionID != taskSnapshot.CriterionID.String() {
		return empty, ErrUnavailable
	}
	if err = m.work.AuthorizeVisualReviewOn(ctx, tx, link(snap, actor), taskSnapshot.ResultID, taskSnapshot.ResultVersion, taskSnapshot.CriterionID, pinned.HeadCommit); err != nil {
		return empty, err
	}
	row, err := m.rowOn(ctx, tx, tenant, execution, true)
	if err != nil {
		return empty, err
	}
	if row.State != "SUCCEEDED" || row.PlanHash != snap.PlanHash {
		return empty, ErrFenced
	}
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution), q.ID.Eq(in.TaskID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if r.State != "DONE" || r.PlanHash != in.PlanHash || r.PlanHash != taskSnapshot.PlanHash || r.ReceiptHash != in.CaptureReceiptHash {
		return empty, ErrFenced
	}
	var p VerificationPlan
	var o VerificationObservation
	if hash([]byte(r.PlanJSON)) != r.PlanHash || hash([]byte(r.ObservationJSON)) != r.ReceiptHash || json.Unmarshal([]byte(r.PlanJSON), &p) != nil || json.Unmarshal([]byte(r.ObservationJSON), &o) != nil || o.PlanHash != r.PlanHash {
		return empty, ErrUnavailable
	}
	_, reason := visualVerificationVerdict(p, o)
	if p.Method != "VISUAL" || reason != "VISUAL_REVIEW_REQUIRED" {
		return empty, ErrConflict
	}
	evidence := query.BindEvidence(tx, m.names)
	for _, screenshot := range o.Screenshots {
		count, e := evidence.WithContext(ctx).Where(evidence.TenantID.Eq(tenant), evidence.ExecutionID.Eq(execution), evidence.Kind.Eq("FILE"), evidence.ContentHash.Eq(screenshot.SHA256)).Count()
		if e != nil {
			return empty, e
		}
		if count != 1 {
			return empty, ErrConflict
		}
	}
	prior, _, err := m.visualReviewOn(ctx, tx, r)
	if err != nil {
		return empty, err
	}
	if prior != nil {
		if prior.VisualReview != in || prior.ReviewerID != actor {
			return empty, ErrConflict
		}
		return *prior, nil
	}
	d.HeadCommit, d.ResultID, d.ResultVersion, d.CriterionID, d.DecidedAt = p.HeadCommit, r.ResultID, r.ResultVersion, r.CriterionID, time.Now().UTC().Truncate(time.Millisecond)
	encoded, err := json.Marshal(d)
	if err != nil {
		return empty, err
	}
	id, err := m.nextID()
	if err != nil {
		return empty, err
	}
	if err = query.BindEvidence(tx, m.names).WithContext(ctx).Create(&model.Evidence{ID: id, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: execution, Kind: "VISUAL_REVIEW", Title: visualReviewTitle(r.ID), Content: string(encoded), ContentHash: hash(encoded), CreatedAt: d.DecidedAt}); err != nil {
		return empty, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.verification_completed", d.ReviewerKind, "permission:verification_run", map[string]any{"task_id": r.ID.String(), "verdict": d.Verdict, "reason": d.Reason, "receipt_hash": hash(encoded), "capture_receipt_hash": r.ReceiptHash, "reviewer_id": actor.String()}); err != nil {
		return empty, err
	}
	return d, nil
}
