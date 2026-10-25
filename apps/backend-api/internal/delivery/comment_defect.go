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
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type commentDefectError struct {
	Reason   string
	TargetID guid.ID
}

func (e *commentDefectError) Error() string {
	return "delivery: artifact comment conversion " + e.Reason
}

type CommentDefectCreate struct {
	Title             string   `json:"title,omitempty" maxLength:"200"`
	Severity          string   `json:"severity,omitempty" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority          *string  `json:"priority,omitempty" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Steps             string   `json:"steps,omitempty" maxLength:"20000"`
	Expected          string   `json:"expected,omitempty" maxLength:"2000"`
	Actual            string   `json:"actual,omitempty" maxLength:"2000"`
	FoundInWorkItemID *guid.ID `json:"found_in_work_item_id,omitempty"`
	SeenSeq           *int64   `json:"seen_seq,omitempty" minimum:"0" doc:"Required for Agent commands; last observed activity sequence of the source work item."`
}
type CommentDefectView struct {
	ID                guid.ID                `json:"id"`
	ProjectID         guid.ID                `json:"project_id"`
	ThreadID          guid.ID                `json:"source_comment_thread_id"`
	ExecutionID       guid.ID                `json:"execution_id"`
	SourceFirstBody   string                 `json:"source_first_body"`
	FoundInWorkItemID guid.ID                `json:"found_in_work_item_id"`
	Anchor            ArtifactAnchorSnapshot `json:"source_anchor_snapshot"`
	Steps             string                 `json:"steps"`
	Expected          string                 `json:"expected"`
	Actual            string                 `json:"actual"`
	FoundStage        string                 `json:"found_stage"`
}

func shortComment(value string, n int) string {
	r := []rune(value)
	if len(r) > n {
		return string(r[:n])
	}
	return value
}

// commentDefectAuthorityOn locks every principal before current project IAM.
// Metadata is re-read under the project/work lock before canonical creation.
func (m *Module) commentDefectAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, thread guid.ID) (ArtifactCommentSource, *model.WorkItem, string, error) {
	var source ArtifactCommentSource
	if !isWorkActivityTransaction(tx) || tenant <= 0 || thread <= 0 {
		return source, nil, "", ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return source, nil, "", ErrWorkForbidden
	}
	if m.artifactComments.ScopeOn == nil || m.artifactComments.GetOn == nil || m.principals.ActiveHumanByID == nil || m.principals.Lock == nil || m.members == nil || m.workPolicy == nil {
		return source, nil, "", ErrUnavailable
	}
	scope, err := m.artifactComments.ScopeOn(ctx, tx, tenant, thread)
	if err != nil {
		return source, nil, "", err
	}
	w := query.BindWorkItems(tx, m.service.names)
	hint, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(scope.ProjectID), w.ID.Eq(scope.WorkItemID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return source, nil, "", err
	}
	ids := []guid.ID{actor, hint.AccountablePrincipalID}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return source, nil, "", err
		}
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return source, nil, "", err
	}
	kind := "HUMAN"
	if !active || human.ID != actor {
		if m.agents.ByPrincipalOn == nil {
			return source, nil, "", ErrWorkForbidden
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return source, nil, "", e
		}
		if !found || agent.TenantID != tenant || agent.PrincipalID != actor || agent.State != "ACTIVE" {
			return source, nil, "", ErrWorkForbidden
		}
		kind = "AGENT"
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return source, nil, "", err
	}
	if !active {
		return source, nil, "", ErrWorkForbidden
	}
	c, err := m.workPolicy(ctx, tx, tenant, scope.ProjectID, "work_item_write", actor)
	if err != nil {
		return source, nil, "", err
	}
	if !c.AllowsEntityRows(scope.ProjectID) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, scope.ProjectID.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return source, nil, "", ErrWorkForbidden
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(scope.ProjectID), p.ArchivedAt.IsNull()).First()
	if err != nil {
		return source, nil, "", err
	}
	if project.Stage == "CLOSED" || project.Stage == "CANCELLED" { //nolint:misspell // Frozen persisted protocol state.
		return source, nil, "", ErrWorkProjectReadOnly
	}
	work, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(scope.ProjectID), w.ID.Eq(scope.WorkItemID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return source, nil, "", err
	}
	if work.AccountablePrincipalID != hint.AccountablePrincipalID {
		return source, nil, "", ErrWorkVersion
	}
	source, err = m.artifactComments.GetOn(ctx, tx, tenant, thread)
	if err != nil {
		return source, nil, "", err
	}
	if source.ProjectID != scope.ProjectID || source.WorkItemID != work.ID {
		return source, nil, "", ErrWorkForbidden
	}
	return source, work, kind, nil
}
func (m *Module) createCommentDefectOn(ctx context.Context, tx *gorm.DB, tenant, thread guid.ID, in CommentDefectCreate) (CommentDefectView, error) {
	var out CommentDefectView
	source, work, kind, err := m.commentDefectAuthorityOn(ctx, tx, tenant, thread)
	if err != nil {
		return out, err
	}
	if source.Visibility != "INTERNAL" {
		return out, &commentDefectError{Reason: "CLIENT_THREAD"}
	}
	if source.DefectID > 0 {
		return out, &commentDefectError{Reason: "ALREADY_CONVERTED", TargetID: source.DefectID}
	}
	if kind == "AGENT" && in.SeenSeq == nil {
		return out, ErrWorkSeenSeqRequired
	}
	if err = m.checkWorkSeenOn(ctx, tx, work, in.SeenSeq); err != nil {
		return out, err
	}
	if !slices.Contains([]string{"FATAL", "CRITICAL", "NORMAL", "MINOR"}, in.Severity) || !utf8.ValidString(in.Expected) || strings.TrimSpace(in.Expected) == "" || utf8.RuneCountInString(in.Expected) > 2000 {
		return out, &commentDefectError{Reason: "DEFECT_FIELDS_REQUIRED"}
	}
	foundInWork := work.ID
	if in.FoundInWorkItemID != nil {
		foundInWork = *in.FoundInWorkItemID
		w := query.BindWorkItems(tx, m.service.names)
		if foundInWork <= 0 {
			return out, ErrInvalidArgument
		}
		if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(source.ProjectID), w.ID.Eq(foundInWork), w.ArchivedAt.IsNull()).First(); err != nil {
			return out, err
		}
	}
	if !slices.Contains([]string{"CODE_LINES", "TABLE_ROW", "TEXT_PARAGRAPH", "HTML_REGION"}, source.Anchor.Kind) || source.Anchor.FileID <= 0 || source.Anchor.VersionID <= 0 || len(source.Anchor.SHA256) != 64 || source.Anchor.VersionRef == "" {
		return out, ErrUnavailable
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = shortComment(source.FirstBody, 80)
	}
	steps := in.Steps
	if steps == "" {
		steps = fmt.Sprintf("%s\n%s\n%s", commentFeedbackLocation(source.Anchor), source.Anchor.VersionRef, source.Anchor.Excerpt)
	}
	actual := in.Actual
	if actual == "" {
		actual = shortComment(source.FirstBody, 2000)
	}
	if !utf8.ValidString(actual) || utf8.RuneCountInString(actual) > 2000 || !utf8.ValidString(steps) || utf8.RuneCountInString(steps) > 20000 {
		return out, ErrInvalidArgument
	}
	version := source.Anchor.SHA256
	priority, reproducibility := "MEDIUM", "UNABLE"
	if in.Priority != nil {
		priority = *in.Priority
	}
	input := workCreateBody{Title: title, Description: steps, Type: "BUG", Objective: in.Expected, AccountablePrincipalID: work.AccountablePrincipalID, Severity: &in.Severity, Priority: &priority, Reproducibility: &reproducibility, FoundVersion: &version, Criteria: []workCriterionInput{{Text: in.Expected, VerificationMethod: "Reproduce using the frozen artifact source and verify the expected result."}}}
	created, err := m.createWorkWithActorOn(ctx, tx, tenant, source.ProjectID, authn.PrincipalIDFromContext(ctx), input, true)
	if err != nil {
		return out, err
	}
	anchor, err := json.Marshal(source.Anchor)
	if err != nil {
		return out, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	stage := work.State
	if work.DefectStage != nil {
		stage = *work.DefectStage
	}
	receipt := model.CommentDefect{ID: guid.ID(id), TenantID: tenant, ProjectID: source.ProjectID, WorkItemID: created.ID, ThreadID: thread, ExecutionID: source.ExecutionID, SourceFirstBody: source.FirstBody, FoundInWorkItemID: foundInWork, AnchorSnapshot: string(anchor), Steps: steps, Expected: in.Expected, Actual: actual, FoundStage: stage, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
	if err = tx.WithContext(ctx).Table(m.service.names.CommentDefects.String()).Create(&receipt).Error; err != nil {
		return out, err
	}
	if err = m.artifactComments.MarkConvertedOn(ctx, tx, tenant, thread, "DEFECT", created.ID, receipt.CreatedBy); err != nil {
		return out, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.defect_created_from_comment", "work_item", created.ID)
	event.Detail["source_comment_thread_id"] = thread.String()
	event.Detail["source_version_id"] = source.Anchor.VersionID.String()
	event.Detail["source_kind"] = source.Anchor.Kind
	event.Detail["project_id"] = source.ProjectID.String()
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	for _, target := range []guid.ID{work.ID, created.ID} {
		if _, err = m.recordWorkActivityOn(ctx, tx, tenant, target, WorkActivityInput{Kind: "DECISION_MADE", ActorKind: kind, ActorID: receipt.CreatedBy, RefKind: "artifact_comment", RefID: thread, Title: "Artifact comment converted to defect"}); err != nil {
			return out, err
		}
	}
	return CommentDefectView{ID: created.ID, ProjectID: source.ProjectID, ThreadID: thread, ExecutionID: source.ExecutionID, SourceFirstBody: source.FirstBody, FoundInWorkItemID: foundInWork, Anchor: source.Anchor, Steps: steps, Expected: in.Expected, Actual: actual, FoundStage: stage}, nil
}

func (m *Module) commentDefectProvenanceOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (CommentDefectView, error) {
	var out CommentDefectView
	actor := authn.PrincipalIDFromContext(ctx)
	q := query.BindWorkItems(tx, m.service.names)
	hint, err := q.WithContext(ctx).Select(q.AccountablePrincipalID).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return out, err
	}
	if _, _, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, work, actor, hint.AccountablePrincipalID, "work_item_read", true); err != nil {
		return out, err
	}
	var row model.CommentDefect
	err = tx.WithContext(ctx).Table(m.service.names.CommentDefects.String()).Where("tenant_id = ? AND project_id = ? AND work_item_id = ?", tenant, project, work).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrProjectNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(row.AnchorSnapshot), &out.Anchor) != nil {
		return out, ErrUnavailable
	}
	out.ID = row.WorkItemID
	out.ProjectID = row.ProjectID
	out.ThreadID = row.ThreadID
	out.ExecutionID = row.ExecutionID
	out.SourceFirstBody = row.SourceFirstBody
	out.FoundInWorkItemID = row.FoundInWorkItemID
	out.Steps = row.Steps
	out.Expected = row.Expected
	out.Actual = row.Actual
	out.FoundStage = row.FoundStage
	return out, nil
}
