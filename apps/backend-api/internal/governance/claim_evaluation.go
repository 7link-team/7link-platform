package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimEvaluationFacts comes from the completion owner inside the subscriber
// transaction. An unavailable diff is explicit and always freezes delegation
// when the sensitive-path rule is enabled.
type ClaimEvaluationFacts struct {
	ClaimID, WorkItemID guid.ID
	SelfConfidence      *int
}

type ClaimEvaluationSource struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (ClaimEvaluationFacts, bool, error)
}

type ClaimEvaluationConnector struct {
	Bind func(ClaimEvaluationSource) error
}

func (m *Module) bindClaimEvaluationSource(source ClaimEvaluationSource) error {
	if source.ReadOn == nil || m.claimSource.ReadOn != nil {
		return ErrInvalidArgument
	}
	m.claimSource = source
	return nil
}

// evaluateClaimEvent uses the existing result lifecycle as well as the frozen
// claim event. No HTTP caller can create a receipt or assert a passing diff.
func (m *Module) evaluateClaimEvent(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if e.EventType != "claim.submitted" && e.EventType != "work_item.state_changed" {
		return ErrInvalidArgument
	}
	var data struct {
		To         string  `json:"to"`
		ResultID   guid.ID `json:"result_id"`
		ClaimID    guid.ID `json:"claim_id"`
		WorkItemID guid.ID `json:"work_item_id"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return ErrInvalidArgument
	}
	if e.EventType == "work_item.state_changed" && data.To != "NEEDS_HUMAN_REVIEW" {
		return nil
	}
	claim, work := data.ClaimID, data.WorkItemID
	if e.EventType == "work_item.state_changed" {
		claim, work = data.ResultID, e.Subject.ID
	}
	if claim <= 0 || work <= 0 || e.TenantID <= 0 || e.ProjectID <= 0 || e.EventID <= 0 {
		return ErrInvalidArgument
	}
	if m == nil || m.seed == nil || m.seed.ids == nil || m.claimSource.ReadOn == nil {
		return ErrUnavailable
	}
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	q := query.BindClaimEvaluations(tx, m.seed.names)
	old, err := q.WithContext(ctx).Where(q.TenantID.Eq(e.TenantID), q.ClaimID.Eq(claim)).First()
	if err == nil {
		if old.ProjectID != e.ProjectID || old.WorkItemID != work {
			return ErrInvalidArgument
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	f, found, err := m.claimSource.ReadOn(ctx, tx, e.TenantID, e.ProjectID, work, claim)
	if err != nil {
		return err
	}
	if !found {
		return nil
	} // Superseded/deleted claims cannot authorize anything.
	if f.ClaimID != claim || f.WorkItemID != work || (f.SelfConfidence != nil && (*f.SelfConfidence < 0 || *f.SelfConfidence > 100)) {
		return ErrInvalidArgument
	}
	rules, err := m.readEffectiveRulesOn(ctx, tx, e.TenantID, e.ProjectID)
	if err != nil {
		return err
	}
	low, sensitive := "SKIPPED", "SKIPPED"
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		triggered := false
		facts := map[string]any{"claim_id": claim.String(), "work_item_id": work.String()}
		switch rule.Kind {
		case "LOW_SELF_CONFIDENCE":
			var params struct {
				Minimum int `json:"min_confidence"`
			}
			if json.Unmarshal(rule.Params, &params) != nil || params.Minimum < 30 || params.Minimum > 90 {
				return ErrUnavailable
			}
			if f.SelfConfidence != nil {
				low = "PASS"
				if *f.SelfConfidence < params.Minimum {
					low, triggered = "TRIGGERED", true
				}
				facts["self_confidence"], facts["min_confidence"] = *f.SelfConfidence, params.Minimum
			}
		case "SENSITIVE_PATH":
			// The execution owner has no DiffSummary capability in the current
			// backend. Per BR-08-28 this is UNAVAILABLE, never an empty passing diff.
			sensitive, triggered = "UNAVAILABLE", true
			facts["paths_unavailable"] = true
		default:
			continue
		}
		if triggered {
			_, err = m.recordEscalationOn(ctx, tx, EscalationFact{TenantID: e.TenantID, ProjectID: e.ProjectID, SubjectID: work, TriggerEventID: e.EventID, Kind: rule.Kind, SubjectKind: "WORK_ITEM", Reason: strings.ToLower(rule.Kind), Facts: facts, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:escalation_evaluator"}})
			if err != nil {
				return err
			}
		}
	}
	id, err := m.seed.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	return q.WithContext(ctx).Create(&model.ClaimEvaluation{ID: guid.ID(id), TenantID: e.TenantID, ProjectID: e.ProjectID, WorkItemID: work, ClaimID: claim, TriggerEventID: e.EventID, LowConfidenceResult: low, SensitivePathResult: sensitive, EvaluatedAt: m.seed.now()})
}

func (m *Module) claimEvaluatedOn(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (bool, error) {
	if req.Facts == nil || req.Facts.ClaimID <= 0 {
		return false, nil
	}
	q := query.BindClaimEvaluations(tx, m.seed.names)
	_, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(req.TenantID), q.ProjectID.Eq(req.ProjectID), q.WorkItemID.Eq(req.Resource.ID), q.ClaimID.Eq(req.Facts.ClaimID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}
