package delivery

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type scopeReworkBody struct {
	SeenSeq         *int64               `json:"seen_seq,omitempty" minimum:"0"`
	FailureCategory string               `json:"failure_category" enum:"SCOPE_CHANGE"`
	Reason          string               `json:"reason" minLength:"5" maxLength:"2000"`
	Feedback        []resultFeedbackBody `json:"feedback" minItems:"1" maxItems:"20"`
}

// T17 is intentionally limited to a stale current Human result waiting for
// review. BR02-33 preserves ACCEPTED work; changed accepted scope needs new work
// or the acceptance owner's formal rework intake, not this command.
func (m *Module) scopeReworkOn(ctx context.Context, tx *gorm.DB, tenant, project, id, result, actor, owner guid.ID, expected int, in scopeReworkBody) (ResultView, error) {
	var empty ResultView
	reason, err := resultText(in.Reason)
	if err != nil || utf8.RuneCountInString(reason) < 5 || in.FailureCategory != "SCOPE_CHANGE" || len(in.Feedback) < 1 || len(in.Feedback) > 20 {
		return empty, ErrInvalidArgument
	}
	work, row, err := m.lockResultActorOn(ctx, tx, tenant, project, id, result, actor, owner, expected, "work_item_read", false, in.SeenSeq)
	if err != nil {
		return empty, err
	}
	anchors := make([]*string, len(in.Feedback))
	for i := range in.Feedback {
		f := &in.Feedback[i]
		anchors[i], err = m.hydrateCommentFeedbackOn(ctx, tx, tenant, project, id, f)
		if err != nil {
			return empty, err
		}
		f.Detail = strings.TrimSpace(f.Detail)
		if !utf8.ValidString(f.Location) || utf8.RuneCountInString(f.Location) > 2000 || !utf8.ValidString(f.Expected) || utf8.RuneCountInString(f.Expected) > 2000 {
			return empty, ErrInvalidArgument
		}
		if f.Kind != "FEEDBACK" || f.Category != "SCOPE_CHANGE" || !utf8.ValidString(f.Detail) || utf8.RuneCountInString(f.Detail) < 5 || utf8.RuneCountInString(f.Detail) > 2000 {
			return empty, ErrInvalidArgument
		}
	}
	if work.State != "NEEDS_HUMAN_REVIEW" || !work.BaselineStale || work.CurrentExecutionID != nil {
		return empty, ErrResultState
	}
	if row.ActorID == actor {
		return empty, ErrResultIndependent
	}
	source, err := m.resultAuthorityOn(ctx, tx, work, row, actor, "decision_request_changes")
	if err != nil {
		return empty, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = m.appendResultActionOn(ctx, tx, work, row, actor, "RETURN", "FAILED", reason, source, nil, now); err != nil {
		return empty, err
	}
	for i, f := range in.Feedback {
		value, e := m.ids.Next()
		if e != nil {
			return empty, e
		}
		record := &model.ResultFeedback{TenantID: tenant, ID: guid.ID(value), ProjectID: project, WorkItemID: id, ResultVersionID: result, Kind: f.Kind, Category: f.Category, Detail: f.Detail, ActorID: actor, CreatedAt: now}
		record.CommentThreadID, record.AnchorSnapshot, record.Location, record.Expected = f.CommentThreadID, anchors[i], f.Location, f.Expected
		if err = query.BindResultFeedback(tx, m.service.names).WithContext(ctx).Create(record); err != nil {
			return empty, err
		}
		if f.CommentThreadID != nil {
			if err = m.artifactComments.MarkConvertedOn(ctx, tx, tenant, *f.CommentThreadID, "FEEDBACK", record.ID, actor); err != nil {
				return empty, err
			}
		}
	}
	before := work.State
	work.State = "REWORK"
	work.DefectStage = nil
	// W6 performs the explicit criterion reselection and immutable contract
	// confirmation next. Until then the old contract remains stale and unaccepted.
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, before, "SCOPE_REQUEST_CHANGES", source, result, now); err != nil {
		return empty, err
	}
	return m.resultViewOn(ctx, tx, row)
}

type workResumeBody struct {
	Reason string `json:"reason" minLength:"5" maxLength:"2000"`
}
type workResumeView struct {
	ID      guid.ID `json:"id"`
	Version int     `json:"version"`
	State   string  `json:"state"`
}

// Human authority opens one more bounded template window. Active runtime
// resumes only through the actual execution hold provider and Runner ACK.
func (m *Module) resumeManualWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, expected int, in workResumeBody) (workResumeView, error) {
	var empty workResumeView
	reason, err := resultText(in.Reason)
	if err != nil || utf8.RuneCountInString(reason) < 5 {
		return empty, ErrInvalidArgument
	}
	verifyFence, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, id, actor, owner)
	if err != nil {
		return empty, err
	}
	_, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "work_item_read")
	if err != nil {
		return empty, err
	}
	if err = verifyFence(); err != nil {
		return empty, err
	}
	if work.Version != expected {
		return empty, ErrWorkVersion
	}
	var blocks []string
	if json.Unmarshal([]byte(work.BlockReasons), &blocks) != nil {
		return empty, ErrUnavailable
	}
	progressMarker := slices.Contains(blocks, "AUTONOMY_NO_PROGRESS") || slices.Contains(blocks, "AUTONOMY_DEPENDENCY_MISSING") || slices.Contains(blocks, "AUTONOMY_SPINNING")
	progressStop := work.PauseReason != nil && progressMarker && slices.Contains([]string{"ESCALATION_RULE", "AUTONOMY_SPINNING"}, *work.PauseReason)
	manualStop := work.State == "REWORK" && work.CurrentExecutionID == nil && work.PauseReason != nil && *work.PauseReason == "AUTONOMY_REWORK_LIMIT"
	if work.PausedAt == nil || (!manualStop && !progressStop) || work.State == "ACCEPTED" || work.State == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return empty, ErrResultState
	}
	source, err := m.manualResumeAuthorityOn(ctx, tx, work, actor)
	if err != nil {
		return empty, err
	}
	work.PausedAt, work.PauseReason, work.PauseDecisionID = nil, nil, nil
	if progressStop {
		blocks = slices.DeleteFunc(blocks, func(s string) bool {
			return slices.Contains([]string{"AUTONOMY_NO_PROGRESS", "AUTONOMY_DEPENDENCY_MISSING", "AUTONOMY_SPINNING"}, s)
		})
		raw, e := json.Marshal(blocks)
		if e != nil {
			return empty, e
		}
		work.BlockReasons = string(raw)
	}
	// Escalation remains Human-only until terminal, per §4.4.
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "WORK_RESUMED", source, 0, time.Now().UTC().Truncate(time.Millisecond)); err != nil {
		return empty, err
	}
	if progressStop && m.noProjectAndonOn(ctx, tx, tenant, project) {
		if work.CurrentExecutionID != nil && m.executionHolds.ChangeOn == nil {
			return empty, ErrUnavailable
		}
		if m.executionHolds.ChangeOn != nil {
			if _, err = m.executionHolds.ChangeOn(ctx, tx, tenant, project, id, true); err != nil {
				return empty, err
			}
		}
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.work_resume_authorized", "work_item", id)
	event.Detail["reason"], event.Detail["authority_source"] = reason, source
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	return workResumeView{ID: id, Version: work.Version, State: work.State}, nil
}

func (m *Module) manualResumeAuthorityOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID) (string, error) {
	if m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	a, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, PrincipalID: actor, ActorKind: "HUMAN", Code: "decision_pause", Resource: governance.ResourceRef{Kind: "work_item", ID: work.ID}, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, RequireHumanDecision: true, Paused: work.PausedAt != nil}})
	if err != nil {
		return "", err
	}
	if !a.Allowed || a.DelegationID != 0 || !strings.HasPrefix(a.AuthoritySource, "ROLE:") {
		return "", ErrWorkForbidden
	}
	return a.AuthoritySource, nil
}
