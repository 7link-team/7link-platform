package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type resultFeedbackBody struct {
	CommentThreadID *guid.ID `json:"comment_thread_id,omitempty"`
	Location        string   `json:"location,omitempty" maxLength:"2000"`
	Expected        string   `json:"expected,omitempty" maxLength:"2000"`
	Kind            string   `json:"kind" enum:"COMMENT,FEEDBACK"`
	Category        string   `json:"category,omitempty" enum:",QUALITY_REJECTION,STRATEGY_CHANGE,SCOPE_CHANGE"`
	Detail          string   `json:"detail,omitempty" maxLength:"2000"`
}
type resultFeedbackView struct {
	CommentThreadID *guid.ID                `json:"comment_thread_id,omitempty"`
	AnchorSnapshot  *ArtifactAnchorSnapshot `json:"anchor_snapshot,omitempty"`
	Location        string                  `json:"location,omitempty"`
	Expected        string                  `json:"expected,omitempty"`
	ID              guid.ID                 `json:"id"`
	ResultVersionID guid.ID                 `json:"result_version_id"`
	Kind            string                  `json:"kind"`
	Category        string                  `json:"category,omitempty"`
	Detail          string                  `json:"detail"`
	ActorID         guid.ID                 `json:"actor_id"`
	CreatedAt       time.Time               `json:"created_at"`
}

func feedbackRecord(r *model.ResultFeedback) resultFeedbackView {
	out := resultFeedbackView{CommentThreadID: r.CommentThreadID, Location: r.Location, Expected: r.Expected, ID: r.ID, ResultVersionID: r.ResultVersionID, Kind: r.Kind, Category: r.Category, Detail: r.Detail, ActorID: r.ActorID, CreatedAt: r.CreatedAt}
	if r.AnchorSnapshot != nil {
		var anchor ArtifactAnchorSnapshot
		if json.Unmarshal([]byte(*r.AnchorSnapshot), &anchor) == nil {
			out.AnchorSnapshot = &anchor
		}
	}
	return out
}

func commentFeedbackLocation(anchor ArtifactAnchorSnapshot) string {
	if anchor.Kind == "CODE_LINES" {
		return fmt.Sprintf("%s:%d-%d", anchor.Path, anchor.StartLine, anchor.EndLine)
	}
	return shortComment(anchor.Path+" · "+anchor.Kind+" · "+string(anchor.Locator), 2000)
}

func (m *Module) hydrateCommentFeedbackOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, in *resultFeedbackBody) (*string, error) {
	if in.CommentThreadID == nil {
		return nil, nil
	}
	if *in.CommentThreadID <= 0 || in.Kind != "FEEDBACK" {
		return nil, ErrInvalidArgument
	}
	if m.artifactComments.GetOn == nil || m.artifactComments.MarkConvertedOn == nil {
		return nil, ErrUnavailable
	}
	source, err := m.artifactComments.GetOn(ctx, tx, tenant, *in.CommentThreadID)
	if err != nil {
		return nil, err
	}
	if source.ProjectID != project || source.WorkItemID != work {
		return nil, &commentDefectError{Reason: "NOT_THIS_WORK_ITEM"}
	}
	if source.Visibility != "INTERNAL" {
		return nil, &commentDefectError{Reason: "CLIENT_THREAD"}
	}
	if source.FeedbackID > 0 {
		return nil, &commentDefectError{Reason: "ALREADY_CONVERTED", TargetID: source.FeedbackID}
	}
	if in.Detail == "" {
		in.Detail = shortComment(source.FirstBody, 2000)
	}
	if in.Location == "" {
		in.Location = commentFeedbackLocation(source.Anchor)
	}
	encoded, err := json.Marshal(source.Anchor)
	if err != nil {
		return nil, err
	}
	value := string(encoded)
	return &value, nil
}

func (m *Module) appendResultFeedbackOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, resultID, actor, owner guid.ID, in resultFeedbackBody) (resultFeedbackView, error) {
	var empty resultFeedbackView
	// A feedback mutation requires active Human membership and current comment write
	// scope. It does not grant a decision, reopen work, or assert client acceptance.
	if _, _, _, err := m.lockedWorkOn(ctx, tx, tenant, project, workID, actor, owner, "artifact_comment_write"); err != nil {
		return empty, err
	}
	anchor, err := m.hydrateCommentFeedbackOn(ctx, tx, tenant, project, workID, &in)
	if err != nil {
		return empty, err
	}
	in.Detail = strings.TrimSpace(in.Detail)
	if !utf8.ValidString(in.Detail) || in.Detail == "" || utf8.RuneCountInString(in.Detail) > 2000 || !utf8.ValidString(in.Location) || utf8.RuneCountInString(in.Location) > 2000 || !utf8.ValidString(in.Expected) || utf8.RuneCountInString(in.Expected) > 2000 || !slices.Contains([]string{"COMMENT", "FEEDBACK"}, in.Kind) || (in.Kind == "COMMENT" && in.Category != "") || (in.Kind == "FEEDBACK" && !slices.Contains([]string{"QUALITY_REJECTION", "STRATEGY_CHANGE", "SCOPE_CHANGE"}, in.Category)) {
		return empty, ErrInvalidArgument
	}
	q := query.BindResultVersions(tx, m.service.names)
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(workID), q.ID.Eq(resultID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrProjectNotFound
	}
	if err != nil {
		return empty, err
	}
	value, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	r := &model.ResultFeedback{TenantID: tenant, ID: guid.ID(value), ProjectID: project, WorkItemID: workID, ResultVersionID: resultID, Kind: in.Kind, Category: in.Category, Detail: in.Detail, ActorID: actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	r.CommentThreadID, r.AnchorSnapshot, r.Location, r.Expected = in.CommentThreadID, anchor, in.Location, in.Expected
	if err = query.BindResultFeedback(tx, m.service.names).WithContext(ctx).Create(r); err != nil {
		return empty, err
	}
	if in.CommentThreadID != nil {
		if err = m.artifactComments.MarkConvertedOn(ctx, tx, tenant, *in.CommentThreadID, "FEEDBACK", r.ID, actor); err != nil {
			return empty, err
		}
	}
	e := audit.NewDomainEvent(ctx, tenant, "delivery.result_feedback_added", "result", resultID)
	e.Detail["feedback_id"] = r.ID.String()
	e.Detail["kind"] = r.Kind
	if err = m.service.audit(ctx, tx, e); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "result.feedback_added", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "permission:artifact_comment_write"}, Subject: events.Subject{Kind: "result", ID: resultID}, Data: map[string]any{"feedback_id": r.ID.String(), "kind": r.Kind, "category": r.Category}})
	if err != nil {
		return empty, err
	}
	return feedbackRecord(r), nil
}

func (m *Module) resultFeedbackOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, resultID, actor guid.ID, offset, limit int) ([]resultFeedbackView, int64, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidArgument
	}
	if _, err := m.readWork(ctx, tx, tenant, project, workID, actor); err != nil {
		return nil, 0, err
	}
	r := query.BindResultVersions(tx, m.service.names)
	if _, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.Eq(workID), r.ID.Eq(resultID)).First(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = ErrProjectNotFound
		}
		return nil, 0, err
	}
	q := query.BindResultFeedback(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(workID), q.ResultVersionID.Eq(resultID))
	total, err := base.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := base.Order(q.CreatedAt, q.ID).Offset(offset).Limit(limit).Find()
	if err != nil {
		return nil, 0, err
	}
	items := make([]resultFeedbackView, 0, len(rows))
	for _, row := range rows {
		items = append(items, feedbackRecord(row))
	}
	return items, total, nil
}
