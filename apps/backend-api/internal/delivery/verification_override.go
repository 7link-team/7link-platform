package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type verificationOverrideBody struct {
	VerificationIDs []guid.ID `json:"verification_ids" minItems:"1" maxItems:"500"`
	EvidenceIDs     []guid.ID `json:"evidence_ids" minItems:"1" maxItems:"500"`
	Reason          string    `json:"reason" minLength:"1" maxLength:"2000"`
	Risk            string    `json:"risk" minLength:"1" maxLength:"2000"`
	ReauthProof     string    `json:"reauth_proof" minLength:"1" maxLength:"512" writeOnly:"true"`
}
type overriddenVerdict struct {
	ID          guid.ID `json:"id"`
	Verdict     string  `json:"verdict"`
	ReceiptHash string  `json:"receipt_hash"`
}
type verificationOverrideView struct {
	ID          guid.ID             `json:"id"`
	ActorID     guid.ID             `json:"actor_id"`
	Reason      string              `json:"reason"`
	Risk        string              `json:"risk"`
	Original    []overriddenVerdict `json:"original_verdicts"`
	EvidenceIDs []guid.ID           `json:"evidence_ids"`
	CreatedAt   time.Time           `json:"created_at"`
}

func (m *Module) resultArtifactHashOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion) (string, []*model.ResultEvidence, error) {
	q := query.BindResultEvidence(tx, m.service.names)
	evidence, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ResultID.Eq(row.ID)).Order(q.ID).Find()
	if err != nil {
		return "", nil, err
	}
	return verificationResultDigest(row, evidence), evidence, nil
}

func (m *Module) verificationOverridesOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion) ([]*model.VerificationOverride, error) {
	q := query.BindVerificationOverrides(tx, m.service.names)
	return q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.WorkItemID.Eq(row.WorkItemID), q.ResultID.Eq(row.ID)).Order(q.ID).Find()
}

func (m *Module) overrideVerificationOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, result, actor, owner guid.ID, expected int, cookie string, in verificationOverrideBody) (ResultView, error) {
	var empty ResultView
	claims, ok := authn.FromContext(ctx)
	if !ok || claims.SubjectType != authn.SubjectTypePrincipal || claims.OAuthGrantID != "" || claims.AccountView != nil {
		return empty, ErrWorkForbidden
	}
	reason, err := resultText(in.Reason)
	if err != nil {
		return empty, err
	}
	risk, err := resultText(in.Risk)
	if err != nil {
		return empty, err
	}
	if len(in.VerificationIDs) == 0 || len(in.VerificationIDs) > 500 || len(in.EvidenceIDs) == 0 || len(in.EvidenceIDs) > 500 || strings.TrimSpace(in.ReauthProof) == "" {
		return empty, ErrInvalidArgument
	}
	if m.scopeReauthentication == nil {
		return empty, ErrUnavailable
	}
	// Prelock all participants in stable order before IAM consumes the proof;
	// IAM itself locks the actor session and must not invert actor/owner order.
	if err = m.lockStagePrincipalsOn(ctx, tx, actor, []guid.ID{owner}); err != nil {
		return empty, err
	}
	// Proof consumption rolls back together with every rejected override.
	if err = m.scopeReauthentication(ctx, tx, actor, cookie, in.ReauthProof, "verification_override", "result:"+result.String()); err != nil {
		return empty, err
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, workID, result, actor, owner, expected, "work_item_read")
	if err != nil {
		return empty, err
	}
	if !slices.Contains([]string{"NEEDS_HUMAN_REVIEW", "INTERNAL_QA", "REWORK"}, work.State) || work.BaselineStale || row.BaselineVersionNo != work.BaselineVersionNo {
		return empty, ErrResultState
	}
	if row.ActorID == actor {
		return empty, ErrResultIndependent
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, row, actor, "decision_force_valid")
	if err != nil {
		return empty, err
	}
	if !strings.HasPrefix(source, "ROLE:") {
		return empty, ErrWorkForbidden
	}
	if _, err = m.resultAuthorityOn(ctx, tx, work, row, actor, "decision_approve"); err != nil {
		return empty, err
	}
	hash, evidence, err := m.resultArtifactHashOn(ctx, tx, row)
	if err != nil {
		return empty, err
	}
	validEvidence := map[guid.ID]bool{}
	for _, e := range evidence {
		validEvidence[e.ID] = true
	}
	seen := map[guid.ID]bool{}
	for _, id := range in.EvidenceIDs {
		if !validEvidence[id] || seen[id] {
			return empty, ErrInvalidArgument
		}
		seen[id] = true
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return empty, err
	}
	selected := map[guid.ID]*model.VerificationRun{}
	for _, r := range runs {
		selected[r.ID] = r
	}
	originals := []overriddenVerdict{}
	seen = map[guid.ID]bool{}
	for _, id := range in.VerificationIDs {
		r := selected[id]
		if r == nil || seen[id] || r.State != "DONE" || r.Method == "HUMAN" || !slices.Contains([]string{"FAILED", "INCONCLUSIVE"}, r.Verdict) {
			return empty, ErrInvalidArgument
		}
		if slices.Contains([]string{"FROZEN_TEST_TAMPERED", "HIDDEN_SUITE_HASH_MISMATCH", "ISOLATION_VIOLATION", "SELF_REVIEW", "PERMISSION_DENIED"}, r.Reason) {
			return empty, ErrWorkForbidden
		}
		originals = append(originals, overriddenVerdict{r.ID, r.Verdict, r.ReceiptHash})
		seen[id] = true
	}
	slices.SortFunc(originals, func(a, b overriddenVerdict) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	raw, _ := json.Marshal(originals)
	ids, _ := json.Marshal(in.EvidenceIDs)
	proof := sha256.Sum256([]byte(in.ReauthProof))
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	override := &model.VerificationOverride{TenantID: tenant, ID: guid.ID(id), ProjectID: project, WorkItemID: workID, ResultID: result, BaselineVersionNo: row.BaselineVersionNo, ArtifactHash: hash, Verdicts: string(raw), Reason: reason, Risk: risk, EvidenceIDs: string(ids), ActorID: actor, AuthoritySource: source, ReauthenticationHash: hex.EncodeToString(proof[:]), CreatedAt: now}
	if err = query.BindVerificationOverrides(tx, m.service.names).WithContext(ctx).Create(override); err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.verification_overridden", "work_item", workID)
	event.Detail["override_id"] = override.ID.String()
	event.Detail["result_id"] = result.String()
	event.Detail["verification_ids"] = in.VerificationIDs
	event.Detail["reason"] = reason
	event.Detail["risk"] = risk
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	if _, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "verification.overridden", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: workID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"work_item_id": workID.String(), "claim_id": result.String(), "override_id": override.ID.String(), "verification_ids": in.VerificationIDs, "artifact_version": hash, "requirement_version": row.BaselineVersionNo}}); err != nil {
		return empty, err
	}
	before := work.State
	work.State = "NEEDS_HUMAN_REVIEW"
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "VERIFICATION_OVERRIDE_RECORDED", source, result, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) verificationAcceptanceOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion) error {
	return m.verificationGateOn(ctx, tx, work, row, false)
}

func (m *Module) verificationAllowedOverridesOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion) (map[guid.ID]overriddenVerdict, error) {
	overrides, err := m.verificationOverridesOn(ctx, tx, row)
	if err != nil {
		return nil, err
	}
	hash, _, err := m.resultArtifactHashOn(ctx, tx, row)
	if err != nil {
		return nil, err
	}
	allowed := map[guid.ID]overriddenVerdict{}
	for _, o := range overrides {
		if o.ArtifactHash != hash || o.BaselineVersionNo != work.BaselineVersionNo {
			continue
		}
		var originals []overriddenVerdict
		if err = json.Unmarshal([]byte(o.Verdicts), &originals); err != nil {
			return nil, err
		}
		for _, original := range originals {
			allowed[original.ID] = original
		}
	}
	return allowed, nil
}

func (m *Module) verificationGateOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, internal bool) error {
	if err := m.hiddenAcceptanceGateOn(ctx, tx, work, row); err != nil {
		return err
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return err
	}
	allowed, err := m.verificationAllowedOverridesOn(ctx, tx, work, row)
	if err != nil {
		return err
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := c.WithContext(ctx).Where(c.TenantID.Eq(work.TenantID), c.ProjectID.Eq(work.ProjectID), c.WorkItemID.Eq(work.ID)).Find()
	if err != nil {
		return err
	}
	humanCriteria := map[guid.ID]bool{}
	var frozenPlan []workVerificationStep
	if err = json.Unmarshal([]byte(row.VerificationPlan), &frozenPlan); err != nil {
		return err
	}
	for _, step := range frozenPlan {
		humanCriteria[step.CriterionID] = step.Method == "HUMAN"
	}
	latest := map[string]bool{}
	covered := map[guid.ID]bool{}
	stages := map[guid.ID]map[string]bool{}
	for _, r := range runs {
		if internal && r.Method == "HUMAN" {
			continue
		}
		key := r.CriterionID.String() + ":" + r.Method + ":" + r.Stage
		if r.Method == "HUMAN" {
			key += ":" + r.ActorID.String()
		}
		if latest[key] {
			continue
		}
		latest[key] = true
		if r.State != "DONE" {
			return ErrVerificationPending
		}
		if r.Verdict != "PASSED" {
			original, ok := allowed[r.ID]
			if !ok || original.Verdict != r.Verdict || original.ReceiptHash != r.ReceiptHash {
				return ErrVerificationPending
			}
		}
		covered[r.CriterionID] = true
		if stages[r.CriterionID] == nil {
			stages[r.CriterionID] = map[string]bool{}
		}
		stages[r.CriterionID][r.Stage] = true
	}
	for _, criterion := range criteria {
		if internal && humanCriteria[criterion.ID] {
			continue
		}
		if !covered[criterion.ID] {
			return ErrVerificationPending
		}
	}
	if row.SourceExecutionID != nil && work.InternalQARequired {
		var plan []workVerificationStep
		if err = json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
			return err
		}
		for _, step := range plan {
			if step.Method == "HUMAN" {
				continue
			}
			found := stages[step.CriterionID]
			if !found["AGENT_TEST"] || !found["AGENT_ACCEPTANCE"] || (step.Method != "AGENT_JUDGE" && !found["AUTOMATED"]) {
				return ErrVerificationPending
			}
		}
	}
	return nil
}

func (m *Module) verificationViewOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion, v *ResultView) error {
	v.Verifications = []verificationView{}
	v.Overrides = []verificationOverrideView{}
	v.Completion.L1 = true
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return err
	}
	for _, r := range runs {
		v.Verifications = append(v.Verifications, verificationView{ID: r.ID, CriterionID: r.CriterionID, Method: r.Method, Stage: r.Stage, Round: r.Round, State: r.State, Verdict: r.Verdict, Reason: r.Reason, ActorID: r.ActorID, TaskID: r.TaskID, CheckID: r.CheckID, ReceiptHash: r.ReceiptHash, ReviewerID: r.ReviewerID, ReviewerModel: r.ReviewerModel, ReviewerFamily: r.ReviewerFamily, FamilyFallback: r.FamilyFallback})
	}
	overrides, err := m.verificationOverridesOn(ctx, tx, row)
	if err != nil {
		return err
	}
	for _, o := range overrides {
		view := verificationOverrideView{ID: o.ID, ActorID: o.ActorID, Reason: o.Reason, Risk: o.Risk, CreatedAt: o.CreatedAt}
		if err = json.Unmarshal([]byte(o.Verdicts), &view.Original); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(o.EvidenceIDs), &view.EvidenceIDs); err != nil {
			return err
		}
		v.Overrides = append(v.Overrides, view)
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(row.TenantID), w.ProjectID.Eq(row.ProjectID), w.ID.Eq(row.WorkItemID)).First()
	if err != nil {
		return err
	}
	v.Completion.InternalQARequired = row.SourceExecutionID != nil && work.InternalQARequired
	v.Completion.HumanOverride = len(v.Overrides) > 0
	// Overrides can permit L3, but never make L2 appear automatically passed.
	v.Completion.L2 = row.SourceExecutionID != nil && len(runs) > 0 && !v.Completion.HumanOverride && m.verificationGateOn(ctx, tx, work, row, true) == nil
	for _, action := range v.Actions {
		if action.Kind == "ACCEPT" {
			v.Completion.L3 = true
		}
		if action.Kind == "RETURN" {
			v.Completion.L3 = false
			v.Completion.L4 = false
		}
		if action.Kind == "RECEIPT" && action.Verdict == "PASSED" {
			v.Completion.L4 = true
		}
	}
	return nil
}
