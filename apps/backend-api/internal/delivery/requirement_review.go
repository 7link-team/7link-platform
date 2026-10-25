package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"time"
)

// RequirementCaseSnapshot captures real case content, never inferred execution results.
type RequirementCaseSnapshot struct {
	RequirementID      *guid.ID `json:"requirement_id,omitempty"`
	CriterionID        *guid.ID `json:"criterion_id,omitempty"`
	CriterionSource    string   `json:"criterion_source,omitempty"`
	Preconditions      string   `json:"preconditions,omitempty"`
	Priority           string   `json:"priority,omitempty"`
	IsHiddenAcceptance bool     `json:"is_hidden_acceptance,omitempty"`
	ContentHash        string   `json:"content_hash,omitempty"`
	ID                 guid.ID  `json:"id"`
	Version            int      `json:"version"`
	WorkItemID         guid.ID  `json:"work_item_id"`
	Title              string   `json:"title"`
	Steps              string   `json:"steps"`
	Expected           string   `json:"expected"`
}
type RequirementCaseReader func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) ([]RequirementCaseSnapshot, error)

var ErrRequirementSuiteMissing = errors.New("delivery: requirement version has no frozen acceptance suite")

// Bound by the downstream case owner to avoid a delivery -> clientwork dependency.
type RequirementSuiteConnector struct{ Bind func(RequirementCaseReader) }
type requirementFrozenSuite struct {
	RequirementID guid.ID                   `json:"requirement_id"`
	VersionNo     int                       `json:"version_no"`
	Cases         []RequirementCaseSnapshot `json:"cases"`
}
type RequirementReviewDecision struct {
	SheetID            guid.ID   `json:"sheet_id"`
	Action             string    `json:"action" enum:"APPROVE,REQUEST_CHANGES"`
	ActorID            guid.ID   `json:"actor_id"`
	AuthoritySource    string    `json:"authority_source"`
	RequirementVersion int       `json:"requirement_version"`
	VersionNo          int       `json:"version_no"`
	ContentHash        string    `json:"content_hash,omitempty"`
	SuiteHash          string    `json:"suite_hash,omitempty"`
	DecidedAt          time.Time `json:"decided_at"`
}

func requirementReviewState(row *model.Requirement) string {
	if row.ReviewState == "" {
		return "NEEDS_WORK"
	}
	return row.ReviewState
}
func (m *Module) freezeRequirementSuiteOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, version *model.RequirementVersion) error {
	w := query.BindWorkItems(tx, m.service.names)
	linked, err := w.WithContext(ctx).Where(w.TenantID.Eq(row.TenantID), w.ProjectID.Eq(row.ProjectID), w.RequirementID.Eq(row.ID), w.ArchivedAt.IsNull()).Order(w.ID).Find()
	if err != nil {
		return err
	}
	ids := make([]guid.ID, 0, len(linked))
	for _, item := range linked {
		ids = append(ids, item.ID)
	}
	cases := []RequirementCaseSnapshot{}
	if len(ids) > 0 {
		if m.requirementCases == nil {
			return ErrUnavailable
		}
		cases, err = m.requirementCases(ctx, tx, row.TenantID, row.ProjectID, ids)
		if err != nil {
			return err
		}
	}
	if cases == nil {
		cases = []RequirementCaseSnapshot{}
	}
	if err := validateHiddenRequirementCases(cases); err != nil {
		return err
	}
	data, err := json.Marshal(requirementFrozenSuite{RequirementID: row.ID, VersionNo: version.VersionNo, Cases: cases})
	if err != nil {
		return err
	}
	// Match MySQL TEXT's byte limit after JSON escaping and UTF-8 encoding.
	// Every dialect rejects the same payload; no frozen content is truncated.
	if len(data) > 65535 {
		return ErrInvalidArgument
	}
	sum := sha256.Sum256(data)
	version.AcceptanceSuite = string(data)
	version.SuiteHash = hex.EncodeToString(sum[:])
	return nil
}
func (m *Module) setRequirementReviewStateOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, state string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if err := m.requirementReviewWriterOn(ctx, tx, row, actor); err != nil {
		return err
	}
	q := query.BindRequirements(tx, m.service.names)
	now := time.Now().UTC().Truncate(time.Millisecond)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).UpdateSimple(q.ReviewState.Value(state), q.Version.Value(row.Version+1), q.UpdatedAt.Value(now), q.UpdatedBy.Value(actor))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrRequirementVersion
	}
	row.ReviewState = state
	row.Version++
	row.UpdatedAt = now
	return nil
}

// The final sheet document is the append-only decision receipt: terminal sheets
// reject every subsequent action; later reviews create a distinct sheet.
func (m *Module) decideRequirementReviewOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, sheet *model.ReviewSheet, actor guid.ID, rejected bool) (*RequirementReviewDecision, error) {
	if err := m.requirementReviewWriterOn(ctx, tx, row, actor); err != nil {
		return nil, err
	}
	action, state := "APPROVE", "APPROVED"
	if rejected {
		action, state = "REQUEST_CHANGES", "NEEDS_WORK"
	}
	authority, err := m.requirementDecisionAuthorityOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, actor, action)
	if err != nil {
		return nil, err
	}
	// Close the marker before baseline invalidation so this decision's own sheet
	// is not canceled by its atomic requirement mutation.
	q := query.BindReviewSheets(tx, m.service.names)
	if _, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(sheet.ID)).UpdateSimple(q.OpenRequirementID.Null()); err != nil {
		return nil, err
	}
	receipt := &RequirementReviewDecision{SheetID: sheet.ID, Action: action, ActorID: actor, AuthoritySource: authority, RequirementVersion: row.Version, VersionNo: row.CurrentVersionNo, DecidedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if !rejected && row.CurrentVersionNo == 0 {
		baseline, err := m.baselineRequirementOn(ctx, tx, row.TenantID, row.ProjectID, row.ID, actor, row.Version)
		if err != nil {
			return nil, err
		}
		row.Version = baseline.Requirement.Version
		row.CurrentVersionNo = baseline.Version.VersionNo
		row.Status = baseline.Requirement.Status
	}
	if row.CurrentVersionNo > 0 {
		v := query.BindRequirementVersions(tx, m.service.names)
		version, err := v.WithContext(ctx).Where(v.TenantID.Eq(row.TenantID), v.ProjectID.Eq(row.ProjectID), v.RequirementID.Eq(row.ID), v.VersionNo.Eq(row.CurrentVersionNo)).First()
		if err != nil {
			return nil, err
		}
		receipt.VersionNo = version.VersionNo
		receipt.ContentHash = version.ContentHash
		receipt.SuiteHash = version.SuiteHash
		if !rejected && version.SuiteHash == "" {
			return nil, ErrRequirementSuiteMissing
		}
		if !rejected {
			if _, err := frozenSuiteCaseCount(version); err != nil {
				return nil, err
			}
		}
	}
	if err := m.setRequirementReviewStateOn(ctx, tx, row, state); err != nil {
		return nil, err
	}
	receipt.RequirementVersion = row.Version
	event := audit.NewDomainEvent(ctx, row.TenantID, "delivery.requirement_decided", "requirement", row.ID)
	event.Detail["action"] = action
	event.Detail["review_sheet_id"] = sheet.ID.String()
	event.Detail["version_no"] = receipt.VersionNo
	event.Detail["suite_hash"] = receipt.SuiteHash
	if err = m.service.audit(ctx, tx, event); err != nil {
		return nil, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "requirement.decided", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: receipt.AuthoritySource}, Subject: events.Subject{Kind: "requirement", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"action": action, "review_sheet_id": sheet.ID.String(), "version_no": receipt.VersionNo, "suite_hash": receipt.SuiteHash}})
	return receipt, err
}

func (m *Module) requirementReviewWriterOn(ctx context.Context, tx *gorm.DB, row *model.Requirement, actor guid.ID) error {
	c, err := m.requirementWriterOn(ctx, tx, row.TenantID, row.ProjectID, actor)
	if err != nil {
		return err
	}
	q := query.BindRequirements(tx, m.service.names)
	scope, err := workScope(c, row.ProjectID, q.CreatedBy)
	if err != nil {
		return err
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(row.ID), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	_, err = base.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWorkForbidden
	}
	return err
}
func frozenSuiteCaseCount(version *model.RequirementVersion) (int, error) {
	if version.AcceptanceSuite == "" || version.AcceptanceSuite == "{}" {
		return 0, nil
	}
	var suite requirementFrozenSuite
	if err := json.Unmarshal([]byte(version.AcceptanceSuite), &suite); err != nil {
		return 0, err
	}
	if suite.VersionNo != version.VersionNo || suite.RequirementID != version.RequirementID {
		return 0, errors.New("delivery: frozen suite version mismatch")
	}
	sum := sha256.Sum256([]byte(version.AcceptanceSuite))
	if hex.EncodeToString(sum[:]) != version.SuiteHash {
		return 0, errors.New("delivery: frozen suite hash mismatch")
	}
	return len(suite.Cases), nil
}

func (m *Module) requirementDecisionAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, requirement, actor guid.ID, action string) (string, error) {
	if m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	code := "decision_approve"
	if action == "REQUEST_CHANGES" {
		code = "decision_request_changes"
	}
	decision, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: "HUMAN", Code: code, Resource: governance.ResourceRef{Kind: "requirement", ID: requirement}})
	if err != nil {
		return "", err
	}
	if !decision.Allowed || decision.AuthoritySource == "" {
		return "", ErrWorkForbidden
	}
	return decision.AuthoritySource, nil
}
