package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrResultState       = errors.New("delivery: result command is unavailable in the current state")
	ErrResultIndependent = errors.New("delivery: an independent Human is required")
	ErrResultCoverage    = errors.New("delivery: exact result needs complete criterion evidence and a passing independent check")
)

const resultEvidencePurpose = "delivery.result_evidence"

type resultEvidenceBody struct {
	SpaceID     guid.ID  `json:"space_id"`
	FileID      guid.ID  `json:"file_id"`
	FileVersion int64    `json:"file_version" minimum:"1"`
	VersionID   guid.ID  `json:"version_id"`
	CriterionID *guid.ID `json:"criterion_id,omitempty"`
	Coverage    string   `json:"coverage" enum:"COVERED,PARTIAL,NOT_APPLICABLE"`
	Note        string   `json:"note,omitempty" maxLength:"2000"`
}
type resultSubmitBody struct {
	SeenSeq  *int64               `json:"seen_seq,omitempty" minimum:"0"`
	Summary  string               `json:"summary" minLength:"1" maxLength:"2000"`
	Evidence []resultEvidenceBody `json:"evidence" minItems:"1" maxItems:"500"`
}
type resultCheckBody struct {
	Feedback []resultFeedbackBody `json:"feedback,omitempty" maxItems:"20"`
	Verdict  string               `json:"verdict" enum:"PASSED,FAILED,INCONCLUSIVE"`
	Reason   string               `json:"reason" minLength:"1" maxLength:"2000"`
}
type resultAcceptBody struct {
	SeenSeq *int64 `json:"seen_seq,omitempty" minimum:"0"`
	Reason  string `json:"reason" minLength:"1" maxLength:"2000"`
}
type resultHandoffBody struct {
	Summary string `json:"summary" minLength:"1" maxLength:"2000"`
}
type resultReceiptBody struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason" minLength:"1" maxLength:"2000"`
}
type resultEvidenceView struct {
	ID          guid.ID  `json:"id"`
	BindingID   guid.ID  `json:"binding_id"`
	SpaceID     guid.ID  `json:"space_id"`
	FileID      guid.ID  `json:"file_id"`
	VersionID   guid.ID  `json:"version_id"`
	CriterionID *guid.ID `json:"criterion_id,omitempty"`
	Coverage    string   `json:"coverage"`
	Note        string   `json:"note"`
	Name        string   `json:"name"`
	SHA256      string   `json:"sha256"`
}
type resultActionView struct {
	AttentionMS *int64    `json:"attention_ms"`
	ID          guid.ID   `json:"id"`
	Kind        string    `json:"kind"`
	Verdict     string    `json:"verdict"`
	ActorID     guid.ID   `json:"actor_id"`
	Reason      string    `json:"reason"`
	HandoffID   *guid.ID  `json:"handoff_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
type resultHandoffView struct {
	ActorID    guid.ID   `json:"actor_id"`
	ID         guid.ID   `json:"id"`
	DeliveryID guid.ID   `json:"delivery_id"`
	ResultID   guid.ID   `json:"result_id"`
	Summary    string    `json:"summary"`
	Manifest   string    `json:"manifest"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
}
type ResultView struct {
	CriteriaMap      []resultCriterionView      `json:"criteria_map"`
	Unverified       []resultUnverifiedView     `json:"unverified"`
	Verifications    []verificationView         `json:"verifications"`
	Overrides        []verificationOverrideView `json:"overrides"`
	Completion       completionLadder           `json:"completion"`
	ID               guid.ID                    `json:"id"`
	ProjectID        guid.ID                    `json:"project_id"`
	WorkItemID       guid.ID                    `json:"work_item_id"`
	Sequence         int                        `json:"sequence"`
	Summary          string                     `json:"summary"`
	ActorID          guid.ID                    `json:"actor_id"`
	PreviousResultID *guid.ID                   `json:"previous_result_id,omitempty"`
	ReworkActionID   *guid.ID                   `json:"rework_action_id,omitempty"`
	CreatedAt        time.Time                  `json:"created_at"`
	Evidence         []resultEvidenceView       `json:"evidence"`
	Actions          []resultActionView         `json:"actions"`
	Handoffs         []resultHandoffView        `json:"handoffs"`
}

func resultText(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 2000 {
		return "", ErrInvalidArgument
	}
	return value, nil
}

// Callback is run inside both pin and read transactions. File root ownership
// alone is never a grant to read the attached business evidence.
func (m *Module) authorizeResultResource(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	tenant, entity, actor := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || entity <= 0 || actor <= 0 || owner.Scope != "entity" || owner.ID != entity {
		return ErrWorkForbidden
	}
	r := query.BindResultVersions(tx, m.service.names)
	row, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	_, err = m.readWork(ctx, tx, tenant, entity, row.WorkItemID, actor)
	return err
}

func (m *Module) resultOn(ctx context.Context, tx *gorm.DB, tenant, project, work, id guid.ID) (*model.ResultVersion, error) {
	r := query.BindResultVersions(tx, m.service.names)
	row, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.Eq(work), r.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	return row, err
}

func (m *Module) resultViewOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion) (ResultView, error) {
	v := ResultView{ID: row.ID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, Sequence: row.Sequence, Summary: row.Summary, ActorID: row.ActorID, PreviousResultID: row.PreviousResultID, ReworkActionID: row.ReworkActionID, CreatedAt: row.CreatedAt, Evidence: []resultEvidenceView{}, Actions: []resultActionView{}, Handoffs: []resultHandoffView{}}
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Where(e.TenantID.Eq(row.TenantID), e.ProjectID.Eq(row.ProjectID), e.ResultID.Eq(row.ID)).Order(e.ID).Find()
	if err != nil {
		return v, err
	}
	for _, item := range evidence {
		v.Evidence = append(v.Evidence, resultEvidenceView{ID: item.ID, BindingID: item.BindingID, SpaceID: item.SpaceID, FileID: item.FileID, VersionID: item.VersionID, CriterionID: item.CriterionID, Coverage: item.Coverage, Note: item.Note, Name: item.Name, SHA256: item.SHA256})
	}
	a := query.BindResultActions(tx, m.service.names)
	actions, err := a.WithContext(ctx).Where(a.TenantID.Eq(row.TenantID), a.ProjectID.Eq(row.ProjectID), a.ResultID.Eq(row.ID)).Order(a.ID).Find()
	if err != nil {
		return v, err
	}
	for _, item := range actions {
		v.Actions = append(v.Actions, resultActionView{AttentionMS: item.AttentionMS, ID: item.ID, Kind: item.Kind, Verdict: item.Verdict, ActorID: item.ActorID, Reason: item.Reason, HandoffID: item.HandoffID, CreatedAt: item.CreatedAt})
	}
	h := query.BindResultHandoffs(tx, m.service.names)
	handoffs, err := h.WithContext(ctx).Where(h.TenantID.Eq(row.TenantID), h.ProjectID.Eq(row.ProjectID), h.ResultID.Eq(row.ID)).Order(h.ID).Find()
	if err != nil {
		return v, err
	}
	for _, item := range handoffs {
		v.Handoffs = append(v.Handoffs, resultHandoffView{ActorID: item.ActorID, ID: item.ID, DeliveryID: item.DeliveryID, ResultID: item.ResultID, Summary: item.Summary, Manifest: item.Manifest, SHA256: item.SHA256, CreatedAt: item.CreatedAt})
	}
	if err := m.verificationViewOn(ctx, tx, row, &v); err != nil {
		return v, err
	}
	if err := resultEvidenceProjection(row, &v); err != nil {
		return v, err
	}
	return v, nil
}

type resultCriterionView struct {
	CriterionID     guid.ID   `json:"criterion_id"`
	Method          string    `json:"method"`
	Coverage        string    `json:"coverage"`
	EvidenceIDs     []guid.ID `json:"evidence_ids"`
	VerificationIDs []guid.ID `json:"verification_ids"`
	Verdict         string    `json:"verdict"`
}

type resultUnverifiedView struct {
	CriterionID guid.ID `json:"criterion_id"`
	Method      string  `json:"method"`
	Reason      string  `json:"reason"`
}

// The immutable plan defines the claim's criterion universe. Current work edits
// and Human exceptions cannot erase gaps or turn an original verdict into pass.
func resultEvidenceProjection(row *model.ResultVersion, v *ResultView) error {
	v.CriteriaMap = []resultCriterionView{}
	v.Unverified = []resultUnverifiedView{}
	var plan []workVerificationStep
	if row.VerificationPlan != "" {
		if err := json.Unmarshal([]byte(row.VerificationPlan), &plan); err != nil {
			return err
		}
	}
	for _, step := range plan {
		item := resultCriterionView{CriterionID: step.CriterionID, Method: step.Method, Coverage: "MISSING", EvidenceIDs: []guid.ID{}, VerificationIDs: []guid.ID{}, Verdict: "PENDING"}
		for _, evidence := range v.Evidence {
			if evidence.CriterionID == nil || *evidence.CriterionID != step.CriterionID {
				continue
			}
			item.EvidenceIDs = append(item.EvidenceIDs, evidence.ID)
			if evidence.Coverage == "COVERED" {
				item.Coverage = "COVERED"
			} else if item.Coverage == "MISSING" && evidence.Coverage == "PARTIAL" {
				item.Coverage = "PARTIAL"
			}
		}
		stage := "AUTOMATED"
		if step.Method == "HUMAN" {
			stage = "HUMAN"
		}
		if step.Method == "AGENT_JUDGE" {
			stage = "AGENT_TEST"
		}
		var latest *verificationView
		for i := range v.Verifications {
			run := &v.Verifications[i]
			if run.CriterionID != step.CriterionID {
				continue
			}
			item.VerificationIDs = append(item.VerificationIDs, run.ID)
			if run.Method == step.Method && run.Stage == stage && (latest == nil || run.ID > latest.ID) {
				latest = run
			}
		}
		if latest != nil && latest.State == "DONE" {
			// A genuine completed run has an executor receipt or immutable Human
			// check source. Pending tasks and pasted logs prove no observation.
			sourced := latest.Method == "HUMAN" && latest.CheckID != nil || latest.Method != "HUMAN" && latest.TaskID != nil && len(latest.ReceiptHash) == 64
			if sourced {
				item.Verdict = latest.Verdict
				item.Coverage = "COVERED"
			}
		}
		reason := ""
		if item.Coverage == "MISSING" {
			reason = "MISSING_EVIDENCE"
		} else if item.Coverage == "PARTIAL" {
			reason = "PARTIAL_EVIDENCE"
		} else if item.Verdict != "PASSED" {
			reason = "VERIFICATION_" + item.Verdict
		}
		if reason != "" {
			v.Unverified = append(v.Unverified, resultUnverifiedView{CriterionID: step.CriterionID, Method: step.Method, Reason: reason})
		}
		v.CriteriaMap = append(v.CriteriaMap, item)
	}
	return nil
}

// Acceptance Agents may defer only criteria explicitly planned for a Human.
// Read through the same projection clients see, from persisted exact-result facts.
func (m *Module) claimAcceptanceEvidenceOn(ctx context.Context, tx *gorm.DB, row *model.ResultVersion) error {
	v, err := m.resultViewOn(ctx, tx, row)
	if err != nil {
		return err
	}
	for _, item := range v.Unverified {
		// Failed observations are evidence, not absent verification. The caller's
		// stage gate separately validates any exact-result Human exception; this
		// presence check must not turn D127 into a fabricated original pass.
		if item.Method != "HUMAN" && item.Reason != "VERIFICATION_FAILED" && item.Reason != "VERIFICATION_INCONCLUSIVE" {
			return ErrVerificationPending
		}
	}
	return nil
}

func (m *Module) submitResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, expected int, input resultSubmitBody) (ResultView, error) {
	var empty ResultView
	summary, err := resultText(input.Summary)
	if err != nil {
		return empty, err
	}
	if len(input.Evidence) < 1 || len(input.Evidence) > 500 {
		return empty, ErrInvalidArgument
	}
	p, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_execute")
	if err != nil {
		return empty, err
	}
	if err = m.checkWorkSeenOn(ctx, tx, work, input.SeenSeq); err != nil {
		return empty, err
	}
	if work.Version != expected {
		return empty, ErrWorkVersion
	}
	if err = m.requireNoAndonOn(ctx, tx, work); err != nil {
		return empty, err
	}
	if p.Stage != "ACTIVE" || work.PausedAt != nil || work.IsContainer || work.CurrentExecutionID != nil || !slices.Contains([]string{"CLAIMED", "IN_PROGRESS", "REWORK", "NEEDS_HUMAN_REVIEW", "ACCEPTED"}, work.State) {
		return empty, ErrResultState
	}
	if m.resultFiles == nil {
		return empty, ErrUnavailable
	}
	// A first submission needs its active unexpired Human lease; replacements
	// remain owned by the original Human and must not impersonate an Execution.
	assign := query.BindAssignments(tx, m.service.names)
	active, err := assign.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(assign.TenantID.Eq(tenant), assign.ProjectID.Eq(project), assign.WorkItemID.Eq(id), assign.Role.Eq("EXECUTOR"), assign.Status.Eq("ACTIVE"), assign.AssigneeKind.Eq("HUMAN"), assign.AssigneePrincipalID.Eq(actor), assign.EnsemblePlanID.IsNull()).First()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if work.CurrentClaimID == nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, ErrAssignmentHolder
		}
		if err != nil {
			return empty, err
		}
		if active.LeaseExpiresAt == nil || !active.LeaseExpiresAt.After(now) {
			return empty, ErrAssignmentState
		}
	} else {
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, err
		}
		previous, e := m.resultOn(ctx, tx, tenant, project, id, *work.CurrentClaimID)
		if e != nil {
			return empty, e
		}
		if previous.ActorID != actor {
			return empty, ErrAssignmentHolder
		}
	}
	r := query.BindResultVersions(tx, m.service.names)
	sequence := 1
	var rework *guid.ID
	if work.CurrentClaimID != nil {
		previous, e := m.resultOn(ctx, tx, tenant, project, id, *work.CurrentClaimID)
		if e != nil {
			return empty, e
		}
		sequence = previous.Sequence + 1
		a := query.BindResultActions(tx, m.service.names)
		last, e := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.ResultID.Eq(previous.ID), a.Kind.Eq("RETURN")).Order(a.ID.Desc()).First()
		if e == nil {
			rework = &last.ID
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return empty, e
		}
	}
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	row := &model.ResultVersion{VerificationPlan: work.VerificationPlan, TenantID: tenant, ID: guid.ID(value), ProjectID: project, WorkItemID: id, Sequence: sequence, Summary: summary, ActorID: actor, BaselineVersionNo: work.BaselineVersionNo, PreviousResultID: work.CurrentClaimID, ReworkActionID: rework, CreatedAt: now}
	if err = r.WithContext(ctx).Create(row); err != nil {
		return empty, err
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.WorkItemID.Eq(id)).Find()
	if err != nil {
		return empty, err
	}
	valid := map[guid.ID]bool{}
	for _, criterion := range criteria {
		valid[criterion.ID] = true
	}
	bindings := map[string]files.PinnedResource{}
	seen := map[string]bool{}
	for _, item := range input.Evidence {
		if item.SpaceID <= 0 || item.FileID <= 0 || item.VersionID <= 0 || item.FileVersion < 1 || !slices.Contains([]string{"COVERED", "PARTIAL", "NOT_APPLICABLE"}, item.Coverage) || !utf8.ValidString(item.Note) || utf8.RuneCountInString(item.Note) > 2000 || (item.Coverage == "NOT_APPLICABLE" && strings.TrimSpace(item.Note) == "") || (item.CriterionID != nil && !valid[*item.CriterionID]) {
			return empty, ErrInvalidArgument
		}
		key := fmt.Sprintf("%d/%d/%d", item.SpaceID, item.FileID, item.VersionID)
		mapping := key + "/general"
		if item.CriterionID != nil {
			mapping = key + "/" + item.CriterionID.String()
		}
		if seen[mapping] {
			return empty, ErrInvalidArgument
		}
		seen[mapping] = true
		eid, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		pin, ok := bindings[key]
		if !ok {
			bid, e := m.ids.Next()
			if e != nil {
				return empty, e
			}
			pin, e = m.resultFiles.PinOn(ctx, tx, files.ResourceBindingInput{ID: guid.ID(bid), SpaceID: item.SpaceID, FileID: item.FileID, ExpectedFileVersion: item.FileVersion, VersionID: &item.VersionID, Purpose: resultEvidencePurpose, ResourceKind: "delivery_result", ResourceID: row.ID, Slot: "evidence." + guid.ID(eid).String(), ActorID: actor})
			if e != nil {
				return empty, e
			}
			bindings[key] = pin
		}
		evidence := &model.ResultEvidence{TenantID: tenant, ID: guid.ID(eid), ProjectID: project, WorkItemID: id, ResultID: row.ID, BindingID: pin.BindingID, SpaceID: item.SpaceID, FileID: item.FileID, VersionID: item.VersionID, CriterionID: item.CriterionID, Coverage: item.Coverage, Note: strings.TrimSpace(item.Note), Name: pin.Name, SHA256: pin.SHA256}
		if e = query.BindResultEvidence(tx, m.service.names).WithContext(ctx).Create(evidence); e != nil {
			return empty, e
		}
	}
	before := work.State
	work.CurrentClaimID = &row.ID
	work.AcceptedAt = nil
	work.AcceptedDecisionID = nil
	work.DefectStage = nil
	work.State = "NEEDS_HUMAN_REVIEW"
	if active != nil && active.Status == "ACTIVE" {
		_, err = assign.WithContext(ctx).Where(assign.TenantID.Eq(tenant), assign.ID.Eq(active.ID), assign.Version.Eq(active.Version)).UpdateSimple(assign.LeaseExpiresAt.Null(), assign.Version.Add(1), assign.UpdatedAt.Value(now))
		if err != nil {
			return empty, err
		}
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "RESULT_SUBMITTED", "permission:work_item_execute", row.ID, now); err != nil {
		return empty, err
	}
	if before == "ACCEPTED" {
		if err = m.recomputeResultDependantsOn(ctx, tx, work, actor, now); err != nil {
			return empty, err
		}
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) resultAuthorityOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, actor guid.ID, code string, deliveryID ...guid.ID) (string, error) {
	if m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	resource := governance.ResourceRef{Kind: "work_item", ID: work.ID}
	if code == "delivery_send" {
		var target guid.ID
		if len(deliveryID) > 0 {
			target = deliveryID[0]
		} else {
			h := query.BindResultHandoffs(tx, m.service.names)
			packageRow, e := h.WithContext(ctx).Where(h.TenantID.Eq(work.TenantID), h.ProjectID.Eq(work.ProjectID), h.WorkItemID.Eq(work.ID), h.ResultID.Eq(row.ID)).Order(h.ID.Desc()).First()
			if e != nil {
				return "", e
			}
			target = packageRow.DeliveryID
		}
		if target <= 0 {
			return "", ErrInvalidArgument
		}
		resource = governance.ResourceRef{Kind: "delivery", ID: target}
	}
	a, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, PrincipalID: actor, ActorKind: "HUMAN", Code: code, Resource: resource, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, SelfProduced: row.ActorID == actor, RequireHumanDecision: true, Paused: work.PausedAt != nil}})
	if err != nil {
		return "", err
	}
	if !a.Allowed || a.AuthoritySource == "" || (code == "decision_force_valid" && a.DelegationID != 0) {
		return "", ErrWorkForbidden
	}
	return a.AuthoritySource, nil
}

func (m *Module) lockResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, permission string) (*model.WorkItem, *model.ResultVersion, error) {
	return m.lockResultActorOn(ctx, tx, tenant, project, id, result, actor, owner, expected, permission, false, nil)
}

func (m *Module) lockResultActorOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, permission string, allowAgent bool, seen *int64) (*model.WorkItem, *model.ResultVersion, error) {
	_, work, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, id, actor, owner, permission, allowAgent)
	if err != nil {
		return nil, nil, err
	}
	if allowAgent {
		kind, err := m.resultActorKindOn(ctx, tx, actor, "")
		if err != nil {
			return nil, nil, err
		}
		if kind == "AGENT" && seen == nil {
			return nil, nil, ErrWorkSeenSeqRequired
		}
	}
	if err = m.checkWorkSeenOn(ctx, tx, work, seen); err != nil {
		return nil, nil, err
	}
	if work.Version != expected {
		return nil, nil, ErrWorkVersion
	}
	if err = m.requireNoAndonOn(ctx, tx, work); err != nil {
		return nil, nil, err
	}
	row, err := m.resultOn(ctx, tx, tenant, project, id, result)
	if err != nil {
		return nil, nil, err
	}
	if work.CurrentClaimID == nil || *work.CurrentClaimID != row.ID || work.PausedAt != nil || work.IsContainer || !slices.Contains([]string{"VERIFYING", "NEEDS_HUMAN_REVIEW", "INTERNAL_QA", "ACCEPTED", "REWORK"}, work.State) {
		return nil, nil, ErrResultState
	}
	return work, row, nil
}

func (m *Module) checkResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, input resultCheckBody) (ResultView, error) {
	var empty ResultView
	reason, err := resultText(input.Reason)
	if err != nil || !slices.Contains([]string{"PASSED", "FAILED", "INCONCLUSIVE"}, input.Verdict) {
		return empty, ErrInvalidArgument
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, id, result, actor, owner, expected, "work_item_write")
	if err != nil {
		return empty, err
	}
	if row.ActorID == actor {
		return empty, ErrResultIndependent
	}
	if work.State != "NEEDS_HUMAN_REVIEW" {
		return empty, ErrResultState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	check, err := m.appendResultActionOn(ctx, tx, work, row, actor, "CHECK", input.Verdict, reason, "permission:work_item_write", nil, now)
	if err != nil {
		return empty, err
	}
	if err = m.recordHumanVerificationOn(ctx, tx, work, row, check); err != nil {
		return empty, err
	}
	if len(input.Feedback) > 20 || (input.Verdict == "PASSED" && len(input.Feedback) > 0) {
		return empty, ErrInvalidArgument
	}
	for _, feedback := range input.Feedback {
		if feedback.Kind != "FEEDBACK" {
			return empty, ErrInvalidArgument
		}
		if _, err = m.appendResultFeedbackOn(ctx, tx, tenant, project, id, result, actor, owner, feedback); err != nil {
			return empty, err
		}
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "RESULT_CHECKED", "permission:work_item_write", row.ID, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) resultAcceptanceReadyOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion) error {
	if err := m.defectDeploymentReadyOn(ctx, tx, work, row); err != nil {
		return err
	}
	if err := m.requireCurrentUpstreamOn(ctx, tx, work); err != nil {
		return err
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := c.WithContext(ctx).Where(c.TenantID.Eq(work.TenantID), c.ProjectID.Eq(work.ProjectID), c.WorkItemID.Eq(work.ID)).Find()
	if err != nil {
		return err
	}
	e := query.BindResultEvidence(tx, m.service.names)
	evidence, err := e.WithContext(ctx).Where(e.TenantID.Eq(work.TenantID), e.ProjectID.Eq(work.ProjectID), e.ResultID.Eq(row.ID)).Find()
	if err != nil {
		return err
	}
	covered := map[guid.ID]bool{}
	for _, item := range evidence {
		if item.CriterionID != nil && (item.Coverage == "COVERED" || item.Coverage == "NOT_APPLICABLE") {
			covered[*item.CriterionID] = true
		}
	}
	if len(criteria) == 0 || len(evidence) == 0 || work.BaselineStale || row.BaselineVersionNo != work.BaselineVersionNo {
		return ErrResultCoverage
	}
	for _, item := range criteria {
		if !covered[item.ID] {
			return ErrResultCoverage
		}
	}
	a := query.BindResultActions(tx, m.service.names)
	checks, err := a.WithContext(ctx).Where(a.TenantID.Eq(work.TenantID), a.ProjectID.Eq(work.ProjectID), a.ResultID.Eq(row.ID), a.Kind.Eq("CHECK")).Order(a.ID.Desc()).Find()
	if err != nil {
		return err
	}
	// Each reviewer's latest check counts; a second reviewer cannot silently
	// overwrite somebody else's unresolved failure or inconclusive finding.
	latest := map[guid.ID]bool{}
	for _, check := range checks {
		if latest[check.ActorID] {
			continue
		}
		latest[check.ActorID] = true
		if check.ActorID == row.ActorID || check.Verdict != "PASSED" {
			return ErrResultCoverage
		}
	}
	if len(latest) == 0 {
		return ErrResultCoverage
	}
	return m.verificationAcceptanceOn(ctx, tx, work, row)
}

func (m *Module) acceptResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, input resultAcceptBody) (ResultView, error) {
	var empty ResultView
	reason, err := resultText(input.Reason)
	if err != nil {
		return empty, err
	}
	work, row, err := m.lockResultActorOn(ctx, tx, tenant, project, id, result, actor, owner, expected, "work_item_read", true, input.SeenSeq)
	if err != nil {
		return empty, err
	}
	if row.ActorID == actor {
		return empty, ErrResultIndependent
	}
	if work.State != "NEEDS_HUMAN_REVIEW" {
		return empty, ErrResultState
	}
	source, err := m.resultAcceptanceAuthorityOn(ctx, tx, work, row, actor)
	if err != nil {
		return empty, err
	}
	if err = m.resultAcceptanceReadyOn(ctx, tx, work, row); err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	action, err := m.appendResultActionOn(ctx, tx, work, row, actor, "ACCEPT", "PASSED", reason, source, nil, now)
	if err != nil {
		return empty, err
	}
	before := work.State
	work.State = "ACCEPTED"
	if isRuntimeDefect(work, row) {
		if work.DefectStage == nil || *work.DefectStage != "VERIFIED" || work.IntroducedStage == nil || *work.IntroducedStage == "" || work.DefectDetails == nil {
			return empty, ErrWorkPreparationState
		}
		if !defectFixed(work) {
			return empty, ErrWorkPreparationState
		}
		stage := "CLOSED"
		work.DefectStage = &stage
	}
	work.AcceptedAt = &now
	work.AcceptedDecisionID = &action.ID
	if work.FirstAcceptedAt == nil {
		work.FirstAcceptedAt = &now
		first := work.QualityReworkCount == 0
		work.FirstPass = &first
	}
	a := query.BindAssignments(tx, m.service.names)
	if _, err = a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(id), a.Status.Eq("ACTIVE")).UpdateSimple(a.Status.Value("COMPLETED"), a.ActiveSlot.Null(), a.LeaseExpiresAt.Null(), a.EndedAt.Value(now), a.EndReason.Value("RESULT_ACCEPTED"), a.UpdatedAt.Value(now), a.Version.Add(1)); err != nil {
		return empty, err
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "RESULT_ACCEPTED", source, row.ID, now); err != nil {
		return empty, err
	}
	if err = m.recomputeResultDependantsOn(ctx, tx, work, actor, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) appendResultActionOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, actor guid.ID, kind, verdict, reason, source string, handoff *guid.ID, now time.Time) (*model.ResultAction, error) {
	id, err := m.ids.Next()
	if err != nil {
		return nil, err
	}
	a := &model.ResultAction{TenantID: work.TenantID, ID: guid.ID(id), ProjectID: work.ProjectID, WorkItemID: work.ID, ResultID: row.ID, Kind: kind, Verdict: verdict, ActorID: actor, AuthoritySource: source, Reason: reason, HandoffID: handoff, CreatedAt: now}
	if err = m.closeDecisionAttentionOn(ctx, tx, a, now); err != nil {
		return nil, err
	}
	return a, query.BindResultActions(tx, m.service.names).WithContext(ctx).Create(a)
}

func (m *Module) resultWorkChangedOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID, before, reason, source string, resultID guid.ID, now time.Time) error {
	if err := m.recordReportL3On(ctx, tx, work, before, now); err != nil {
		return err
	}
	expected := work.Version
	work.Version++
	work.StateChangedAt = now
	work.UpdatedAt = now
	work.UpdatedBy = &actor
	actorKind, trigger := "HUMAN", "COMMAND"
	if strings.HasPrefix(source, "DELEGATION:") || (actor > 0 && source == "SYSTEM:dependency_recheck") || reason == "ANDON_PULLED" || reason == "ANDON_CLEARED" || strings.HasPrefix(reason, "REQUIREMENT_CONFLICT_") {
		var err error
		actorKind, err = m.resultActorKindOn(ctx, tx, actor, source)
		if err != nil {
			return err
		}
	}
	var actorID *guid.ID = &actor
	if actor == 0 && (source == "SYSTEM:verification" || source == "SYSTEM:deployment") {
		work.UpdatedBy = nil
		actorID = nil
		actorKind, trigger = "SYSTEM", "SYSTEM"
	}
	activityKind, refKind, refID := "STATE_CHANGED", "result", resultID
	if reason == "RESULT_SUBMITTED" {
		activityKind = "CLAIM_SUBMITTED"
	}
	if slices.Contains([]string{"RESULT_CHECKED", "RESULT_ACCEPTED", "RESULT_RECEIVED", "RESULT_RETURNED"}, reason) {
		activityKind = "DECISION_MADE"
	}
	if refID == 0 || reason == "RESULT_DEPENDENCY_RECHECKED" {
		refKind, refID = "work_item", work.ID
	}
	seq, err := m.recordWorkActivityOn(ctx, tx, work.TenantID, work.ID, WorkActivityInput{Kind: activityKind, ActorKind: actorKind, ActorID: actor, RefKind: refKind, RefID: refID, Title: reason})
	if err != nil {
		return err
	}
	work.ActivitySeq = seq
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(work.TenantID), w.ProjectID.Eq(work.ProjectID), w.ID.Eq(work.ID), w.Version.Eq(expected)).Select(w.State, w.InternalRoundCount, w.EscalationReason, w.CurrentClaimID, w.AcceptedAt, w.AcceptedDecisionID, w.FirstAcceptedAt, w.FirstPass, w.QualityReworkCount, w.DefectStage, w.EscapedFromStage, w.EscapeCount, w.ReadinessOK, w.ReadinessIssues, w.BlockReasons, w.PausedAt, w.PauseReason, w.PauseDecisionID, w.RequireHumanDecision, w.Version, w.StateChangedAt, w.UpdatedAt, w.UpdatedBy).Updates(work)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	tid, err := m.ids.Next()
	if err != nil {
		return err
	}
	ref := "result:" + resultID.String() + ":" + guid.ID(tid).String()
	if resultID == 0 {
		ref = "work_item:" + work.ID.String() + ":" + guid.ID(tid).String()
	}
	transition := &model.WorkItemTransition{TenantID: work.TenantID, ID: guid.ID(tid), ProjectID: work.ProjectID, WorkItemID: work.ID, FromState: &before, ToState: work.State, Trigger: trigger, ActorKind: actorKind, ActorID: actorID, AuthoritySource: source, ReasonCode: reason, SourceRef: &ref, VersionAfter: work.Version, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, work.TenantID, "delivery."+strings.ToLower(reason), "work_item", work.ID)
	if resultID != 0 {
		event.Detail["result_id"] = resultID.String()
	}
	event.Detail["from"] = before
	event.Detail["to"] = work.State
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	data := map[string]any{"from": before, "to": work.State, "reason_code": reason}
	if resultID != 0 {
		data["result_id"] = resultID.String()
	}
	if source == "SYSTEM:verification" && work.State != "NEEDS_HUMAN_REVIEW" {
		return nil
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: work.TenantID, ProjectID: work.ProjectID, Type: "work_item.state_changed", Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: data})
	return err
}

func (m *Module) handoffResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, input resultHandoffBody) (ResultView, error) {
	var empty ResultView
	summary, err := resultText(input.Summary)
	if err != nil {
		return empty, err
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, id, result, actor, owner, expected, "work_item_read")
	if err != nil {
		return empty, err
	}
	if work.State != "ACCEPTED" || work.AcceptedAt == nil || work.AcceptedDecisionID == nil {
		return empty, ErrResultState
	}
	if err = m.resultAcceptanceReadyOn(ctx, tx, work, row); err != nil {
		return empty, err
	}
	view, err := m.resultViewOn(ctx, tx, row)
	if err != nil {
		return empty, err
	}
	// The package contains a canonical immutable manifest of the accepted
	// version and file hashes, not mutable current-head download references.
	manifest, err := json.Marshal(struct {
		ResultID     guid.ID                    `json:"result_id"`
		Sequence     int                        `json:"sequence"`
		AcceptanceID guid.ID                    `json:"acceptance_id"`
		Summary      string                     `json:"summary"`
		Evidence     []resultEvidenceView       `json:"evidence"`
		Overrides    []verificationOverrideView `json:"verification_overrides"`
		CriteriaMap  []resultCriterionView      `json:"criteria_map"`
		Unverified   []resultUnverifiedView     `json:"unverified"`
	}{row.ID, row.Sequence, *work.AcceptedDecisionID, summary, view.Evidence, view.Overrides, view.CriteriaMap, view.Unverified})
	if err != nil {
		return empty, err
	}
	digest := sha256.Sum256(manifest)
	hash := hex.EncodeToString(digest[:])
	now := time.Now().UTC().Truncate(time.Millisecond)
	p := query.BindProjects(tx, m.service.names)
	projectRow, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project)).First()
	if err != nil {
		return empty, err
	}
	did, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	hid, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	delivery := &model.Delivery{TenantID: tenant, ID: guid.ID(did), ProjectID: project, SequenceNo: projectRow.NextDeliverySeq, Number: fmt.Sprintf("D-%d", projectRow.NextDeliverySeq), Title: work.Title, Summary: &summary, State: "READY", BundleState: "NONE", BundleFingerprint: &hash, SendContactIDs: "[]", SourceIntakeIDs: "[]", CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor, Version: 1}
	if err = query.BindDeliveries(tx, m.service.names).WithContext(ctx).Create(delivery); err != nil {
		return empty, err
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, row, actor, "delivery_send", delivery.ID)
	if err != nil {
		return empty, err
	}
	changed, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.Version.Eq(projectRow.Version)).UpdateSimple(p.NextDeliverySeq.Add(1), p.Version.Add(1), p.UpdatedAt.Value(now))
	if err != nil {
		return empty, err
	}
	if changed.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	h := &model.ResultHandoff{TenantID: tenant, ID: guid.ID(hid), ProjectID: project, WorkItemID: id, ResultID: result, DeliveryID: delivery.ID, Summary: summary, Manifest: string(manifest), SHA256: hash, ActorID: actor, CreatedAt: now}
	if err = query.BindResultHandoffs(tx, m.service.names).WithContext(ctx).Create(h); err != nil {
		return empty, err
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "RESULT_HANDOFF_CREATED", source, row.ID, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

func (m *Module) receiveResultOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, handoff, actor, owner guid.ID, expected int, input resultReceiptBody) (ResultView, error) {
	var empty ResultView
	reason, err := resultText(input.Reason)
	if err != nil {
		return empty, err
	}
	work, row, err := m.lockResultOn(ctx, tx, tenant, project, id, result, actor, owner, expected, "work_item_read")
	if err != nil {
		return empty, err
	}
	if work.State != "ACCEPTED" || work.AcceptedAt == nil {
		return empty, ErrResultState
	}
	if input.Accepted {
		if err = m.resultAcceptanceReadyOn(ctx, tx, work, row); err != nil {
			return empty, err
		}
	}
	h := query.BindResultHandoffs(tx, m.service.names)
	packageRow, err := h.WithContext(ctx).Where(h.TenantID.Eq(tenant), h.ProjectID.Eq(project), h.WorkItemID.Eq(id), h.ResultID.Eq(result), h.ID.Eq(handoff)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	if packageRow.ActorID == actor || (!input.Accepted && row.ActorID == actor) {
		return empty, ErrResultIndependent
	}
	code := "delivery_send"
	kind, verdict, event := "RECEIPT", "PASSED", "RESULT_RECEIVED"
	if !input.Accepted {
		code = "decision_request_changes"
		kind = "RETURN"
		verdict = "FAILED"
		event = "RESULT_RETURNED"
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, row, actor, code, packageRow.DeliveryID)
	if err != nil {
		return empty, err
	}
	a := query.BindResultActions(tx, m.service.names)
	count, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.HandoffID.Eq(handoff)).Count()
	if err != nil {
		return empty, err
	}
	if count > 0 {
		return empty, ErrResultState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	action, err := m.appendResultActionOn(ctx, tx, work, row, actor, kind, verdict, reason, source, &handoff, now)
	if err != nil {
		return empty, err
	}
	before := work.State
	state := "ACCEPTED"
	if !input.Accepted {
		state = "REJECTED"
		if err = m.publishQualityEscapeOn(ctx, tx, work, row, action.ID, actor, reason); err != nil {
			return empty, err
		}
		work.State = "REWORK"
		work.AcceptedAt = nil
		work.AcceptedDecisionID = nil
		work.QualityReworkCount++
		work.DefectStage = nil
		if work.QualityReworkCount >= 3 {
			pauseReason := "AUTONOMY_REWORK_LIMIT"
			work.PausedAt = &now
			work.PauseReason = &pauseReason
			work.RequireHumanDecision = true
		}
	}
	d := query.BindDeliveries(tx, m.service.names)
	updated, err := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(project), d.ID.Eq(packageRow.DeliveryID), d.State.Eq("READY")).UpdateSimple(d.State.Value(state), d.AcceptanceID.Value(action.ID), d.ResultAt.Value(now), d.Version.Add(1), d.UpdatedAt.Value(now))
	if err != nil {
		return empty, err
	}
	if updated.RowsAffected != 1 {
		return empty, ErrResultState
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, event, source, row.ID, now); err != nil {
		return empty, err
	}
	if !input.Accepted && work.QualityReworkCount >= 3 {
		escalation := audit.NewDomainEvent(ctx, tenant, "delivery.work_item_escalated", "work_item", work.ID)
		escalation.Detail["trigger"] = "REWORK_LIMIT"
		escalation.Detail["count"] = work.QualityReworkCount
		if err = m.service.audit(ctx, tx, escalation); err != nil {
			return empty, err
		}
		if _, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.escalated", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version), Data: map[string]any{"work_item_id": work.ID.String(), "trigger": "REWORK_LIMIT", "count": work.QualityReworkCount}}); err != nil {
			return empty, err
		}
	}
	if !input.Accepted {
		if err = m.recomputeResultDependantsOn(ctx, tx, work, actor, now); err != nil {
			return empty, err
		}
	}
	return m.resultViewOn(ctx, tx, row)
}

// Project serialization has already been acquired. Only preparation states
// advance; an executing successor keeps its ownership and receives new blockers.
func (m *Module) recomputeResultDependantsOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID, now time.Time) error {
	d := query.BindDependencies(tx, m.service.names)
	edges, err := d.WithContext(ctx).Where(d.TenantID.Eq(work.TenantID), d.ProjectID.Eq(work.ProjectID), d.DependsOnID.Eq(work.ID)).Order(d.WorkItemID).Find()
	if err != nil {
		return err
	}
	w := query.BindWorkItems(tx, m.service.names)
	for _, edge := range edges {
		row, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(work.TenantID), w.ProjectID.Eq(work.ProjectID), w.ID.Eq(edge.WorkItemID), w.ArchivedAt.IsNull()).First()
		if e != nil {
			return e
		}
		if slices.Contains([]string{"ACCEPTED", "CANCELLED", "SKIPPED"}, row.State) { //nolint:misspell // Frozen domain state.
			continue
		}
		human, active, e := m.principals.ActiveHumanByID(ctx, tx, row.AccountablePrincipalID)
		if e != nil {
			return e
		}
		member, e := m.members.IsMemberActiveOn(ctx, tx, row.TenantID, row.AccountablePrincipalID)
		if e != nil {
			return e
		}
		issues, blocks, e := m.evaluatePreparationOn(ctx, tx, row, active && human.ID == row.AccountablePrincipalID && member)
		if e != nil {
			return e
		}
		before := row.State
		row.ReadinessOK = len(issues) == 0
		encoded, _ := json.Marshal(issues)
		row.ReadinessIssues = string(encoded)
		encoded, _ = json.Marshal(blocks)
		row.BlockReasons = string(encoded)
		if row.PausedAt == nil && slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, row.State) {
			switch {
			case len(blocks) > 0:
				row.State = "BLOCKED"
			case row.ReadinessOK:
				row.State = "READY"
			default:
				row.State = "DRAFT"
			}
		}
		if e = m.resultWorkChangedOn(ctx, tx, row, actor, before, "RESULT_DEPENDENCY_RECHECKED", "SYSTEM:dependency_recheck", work.ID, now); e != nil {
			return e
		}
	}
	return nil
}
