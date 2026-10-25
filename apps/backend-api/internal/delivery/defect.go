package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type defectStageBody struct {
	SeenSeq  *int64               `json:"seen_seq,omitempty" minimum:"0"`
	Feedback []resultFeedbackBody `json:"feedback,omitempty" maxItems:"20"`
	ResultID guid.ID              `json:"result_id"`
	Stage    string               `json:"stage" enum:"MERGED,DEPLOYED,VERIFIED,CLOSED,REWORK"`
	Reason   string               `json:"reason" minLength:"5" maxLength:"2000"`
}
type defectStageView struct {
	WorkItemID guid.ID `json:"work_item_id"`
	ResultID   guid.ID `json:"result_id"`
	Stage      string  `json:"stage"`
	Version    int     `json:"version"`
}

// Stage advancement consumes source-owned machine/deployment facts. Manual
// result verification retains the independent accepted-Human checks.
func (m *Module) defectStageOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, expected int, in defectStageBody) (defectStageView, error) {
	var empty defectStageView
	if in.ResultID <= 0 || !slices.Contains([]string{"MERGED", "DEPLOYED", "VERIFIED", "CLOSED", "REWORK"}, in.Stage) {
		return empty, ErrInvalidArgument
	}
	work, result, err := m.lockResultActorOn(ctx, tx, tenant, project, id, in.ResultID, actor, owner, expected, "work_item_write", in.Stage == "MERGED", in.SeenSeq)
	if err != nil {
		return empty, err
	}
	if !slices.Contains([]string{"BUG", "INCIDENT"}, work.Type) {
		return empty, ErrWorkPreparationState
	}
	if in.Stage == "REWORK" {
		return m.rejectDefectOn(ctx, tx, work, result, actor, in)
	}
	if in.Stage == "MERGED" || in.Stage == "DEPLOYED" {
		if _, err = m.trustedDefectMergeOn(ctx, tx, work, result); err != nil {
			return empty, err
		}
		if work.BaselineStale || result.BaselineVersionNo != work.BaselineVersionNo {
			return empty, ErrResultState
		}
		if in.Stage == "DEPLOYED" {
			if err = m.defectDeploymentReadyOn(ctx, tx, work, result); err != nil {
				return empty, err
			}
		} else if work.DefectStage != nil {
			return empty, ErrWorkPreparationState
		}
		reason, err := resultText(in.Reason)
		if err != nil || utf8.RuneCountInString(reason) < 5 {
			return empty, ErrInvalidArgument
		}
		before := work.State
		work.DefectStage = &in.Stage
		if in.Stage == "MERGED" && work.State != "ACCEPTED" {
			work.State = "VERIFYING"
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "DEFECT_"+in.Stage, "permission:work_item_write", result.ID, now); err != nil {
			return empty, err
		}
		event := audit.NewDomainEvent(ctx, tenant, "delivery.defect_stage_changed", "work_item", id)
		event.Detail["reason"] = reason
		event.Detail["stage"] = in.Stage
		event.Detail["result_id"] = result.ID.String()
		if err = m.service.audit(ctx, tx, event); err != nil {
			return empty, err
		}
		return defectStageView{WorkItemID: id, ResultID: result.ID, Stage: in.Stage, Version: work.Version}, nil
	}
	reason, err := resultText(in.Reason)
	if err != nil {
		return empty, err
	}
	if utf8.RuneCountInString(reason) < 5 {
		return empty, ErrInvalidArgument
	}
	if work.State != "ACCEPTED" || work.AcceptedAt == nil || work.AcceptedDecisionID == nil || work.BaselineStale {
		return empty, ErrResultState
	}
	if err = m.resultAcceptanceReadyOn(ctx, tx, work, result); err != nil {
		return empty, err
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, result, actor, "decision_approve")
	if err != nil {
		return empty, err
	}
	a := query.BindResultActions(tx, m.service.names)
	// The verifier must have personally recorded a passing independent check of
	// this exact result. A later failed check by that actor invalidates the guard.
	check, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(id), a.ResultID.Eq(result.ID), a.ActorID.Eq(actor), a.Kind.Eq("CHECK")).Order(a.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrResultIndependent
	}
	if err != nil {
		return empty, err
	}
	if check.Verdict != "PASSED" || actor == result.ActorID {
		return empty, ErrResultIndependent
	}
	if in.Stage == "CLOSED" {
		if work.DefectStage == nil || *work.DefectStage != "VERIFIED" {
			return empty, ErrWorkPreparationState
		}
		transitions := query.BindWorkTransitions(tx, m.service.names)
		if _, err = transitions.WithContext(ctx).Where(transitions.TenantID.Eq(tenant), transitions.ProjectID.Eq(project), transitions.WorkItemID.Eq(id), transitions.SourceRef.Like("result:"+result.ID.String()+":%"), transitions.ReasonCode.Eq("DEFECT_VERIFIED")).First(); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = ErrWorkPreparationState
			}
			return empty, err
		}
	} else if work.DefectStage != nil && !slices.Contains([]string{"MERGED", "DEPLOYED"}, *work.DefectStage) {
		return empty, ErrWorkPreparationState
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	w := query.BindWorkItems(tx, m.service.names)
	updated, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.Version.Eq(expected)).UpdateSimple(w.DefectStage.Value(in.Stage))
	if err != nil {
		return empty, err
	}
	if updated.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	work.DefectStage = &in.Stage
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "DEFECT_"+strings.ToUpper(in.Stage), source, result.ID, now); err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.defect_stage_changed", "work_item", id)
	event.Detail["reason"] = reason
	event.Detail["stage"] = in.Stage
	event.Detail["result_id"] = result.ID.String()
	event.Detail["authority_source"] = source
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	return defectStageView{WorkItemID: id, ResultID: result.ID, Stage: in.Stage, Version: work.Version}, nil
}

func (m *Module) rejectDefectOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, result *model.ResultVersion, actor guid.ID, in defectStageBody) (defectStageView, error) {
	var out defectStageView
	reason, err := resultText(in.Reason)
	if err != nil || utf8.RuneCountInString(reason) < 5 || len(in.Feedback) < 1 || len(in.Feedback) > 20 {
		return out, ErrInvalidArgument
	}
	if work.State != "NEEDS_HUMAN_REVIEW" || work.CurrentClaimID == nil || *work.CurrentClaimID != result.ID {
		return out, ErrResultState
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return out, err
	}
	if !active || human.ID != actor {
		return out, ErrWorkForbidden
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, result, actor, "decision_request_changes")
	if err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	action, err := m.appendResultActionOn(ctx, tx, work, result, actor, "RETURN", "FAILED", reason, source, nil, now)
	if err != nil {
		return out, err
	}
	for _, feedback := range in.Feedback {
		if feedback.Kind != "FEEDBACK" || feedback.Category != "QUALITY_REJECTION" {
			return out, ErrInvalidArgument
		}
		if _, err = m.appendResultFeedbackOn(ctx, tx, work.TenantID, work.ProjectID, work.ID, result.ID, actor, work.AccountablePrincipalID, feedback); err != nil {
			return out, err
		}
	}
	if err = m.humanEscapeOn(ctx, tx, action); err != nil {
		return out, err
	}
	// humanEscapeOn derives receipt evidence independently; synchronize only the
	// source-owned fields it wrote before the aggregate CAS update below.
	w := query.BindWorkItems(tx, m.service.names)
	current, err := w.WithContext(ctx).Where(w.TenantID.Eq(work.TenantID), w.ID.Eq(work.ID)).First()
	if err != nil {
		return out, err
	}
	work.EscapedFromStage = current.EscapedFromStage
	work.EscapeCount = current.EscapeCount
	before := work.State
	work.State = "REWORK"
	work.DefectStage = nil
	work.QualityReworkCount++
	if work.QualityReworkCount >= 3 {
		r := "AUTONOMY_REWORK_LIMIT"
		work.PausedAt = &now
		work.PauseReason = &r
		work.RequireHumanDecision = true
	}
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "DEFECT_REQUEST_CHANGES", source, result.ID, now); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, work.TenantID, "delivery.defect_stage_changed", "work_item", work.ID)
	event.Detail["reason"] = reason
	event.Detail["stage"] = "REWORK"
	event.Detail["result_id"] = result.ID.String()
	event.Detail["authority_source"] = source
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	return defectStageView{WorkItemID: work.ID, ResultID: result.ID, Stage: "REWORK", Version: work.Version}, nil
}

// Keep malformed stored resolution data from being interpreted as FIXED.
func defectFixed(work *model.WorkItem) bool {
	if work.DefectDetails == nil {
		return false
	}
	var details DefectDescription
	return json.Unmarshal([]byte(*work.DefectDetails), &details) == nil && details.Resolution != nil && *details.Resolution == "FIXED"
}
