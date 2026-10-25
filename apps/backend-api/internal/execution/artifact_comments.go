package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// artifactCommentRead is private and scoped to one already-authorized evidence
// read. It cannot be supplied by an HTTP client or authorize other file purposes.
type artifactCommentRead struct{ Tenant, Project, Evidence guid.ID }
type artifactCommentReadKey struct{}

func (m *Module) injectArtifactComments(s *module.Services) error {
	var err error
	if m.commentIdempotency, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.commentFiles, err = module.Resolve[files.PinnedResources](s); err != nil {
		return err
	}
	if m.commentMembers, err = module.Resolve[core.ActiveMemberChecker](s); err != nil {
		return err
	}
	if m.commentActivities, err = module.Resolve[delivery.WorkItemActivityRecorder](s); err != nil {
		return err
	}
	if m.commentContext, err = module.Resolve[delivery.WorkItemActivityReader](s); err != nil {
		return err
	}
	connector, err := module.Resolve[delivery.ArtifactCommentsConnector](s)
	if err != nil {
		return err
	}
	m.commentFeedback = connector.FeedbackOn
	catalog, catalogErr := module.Resolve[agents.MountedOperationCatalog](s)
	if catalogErr == nil {
		if catalog.Register == nil {
			return ErrCapabilityUnavailable
		}
		if err = catalog.Register(
			agents.MountedOperation{Name: "evidence.create", HTTPID: "evidence.create", Permission: "evidence_submit"},
			agents.MountedOperation{Name: "evidence.update", HTTPID: "evidence.update", Permission: "evidence_submit"},
			agents.MountedOperation{Name: "artifact_comment.create", HTTPID: "artifact_comment.create", Permission: "artifact_comment_write"},
			agents.MountedOperation{Name: "artifact_comment_thread.create", HTTPID: "artifact_comment.create", Permission: "artifact_comment_write"},
			agents.MountedOperation{Name: "artifact_comment.list", HTTPID: "artifact_comment.list", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.reply", HTTPID: "artifact_comment.reply", Permission: "artifact_comment_write"},
			agents.MountedOperation{Name: "artifact_comment.messages", HTTPID: "artifact_comment.messages", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.source", HTTPID: "artifact_comment.source", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.state", HTTPID: "artifact_comment.state", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.reanchor", HTTPID: "artifact_comment.reanchor", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.edit", HTTPID: "artifact_comment.edit", Permission: "execution_read"},
			agents.MountedOperation{Name: "artifact_comment.withdraw", HTTPID: "artifact_comment.withdraw", Permission: "execution_read"},
		); err != nil {
			return err
		}
	} else if !errors.Is(catalogErr, module.ErrServiceMissing) {
		return catalogErr
	}
	return connector.Bind(delivery.ArtifactComments{
		ScopeOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (delivery.ArtifactCommentScope, error) {
			out, err := m.artifactCommentScopeOn(ctx, tx, tenant, id)
			return out, artifactCommentDeliveryError(err)
		},
		GetOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (delivery.ArtifactCommentSource, error) {
			out, err := m.artifactCommentSourceOn(ctx, tx, tenant, id)
			return out, artifactCommentDeliveryError(err)
		},
		MarkConvertedOn: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string, target, actor guid.ID) error {
			return artifactCommentDeliveryError(m.markArtifactCommentConvertedOn(ctx, tx, tenant, id, kind, target, actor))
		},
	})
}

func artifactCommentDeliveryError(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return delivery.ErrWorkForbidden
	case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return delivery.ErrProjectNotFound
	case errors.Is(err, ErrInvalidArgument):
		return delivery.ErrInvalidArgument
	case errors.Is(err, ErrUnavailable):
		return delivery.ErrUnavailable
	}
	return err
}

// commentAuthority deliberately does not broaden humanAuthority. It permits
// current active internal Agent principals only for this comment capability.
func (m *Module) commentAuthority(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) (string, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return "", ErrForbidden
	}
	if m.policy == nil || m.commentMembers == nil || m.principals.ActiveHumanByID == nil {
		return "", ErrUnavailable
	}
	if err := m.principalLocks(ctx, tx, actor); err != nil {
		return "", err
	}
	_, human, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return "", err
	}
	kind := "HUMAN"
	if !human {
		if m.agents.ByPrincipalOn == nil {
			return "", ErrForbidden
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, actor)
		if e != nil {
			return "", e
		}
		if !found || agent.TenantID != tenant || agent.PrincipalID != actor || agent.State != "ACTIVE" {
			return "", ErrForbidden
		}
		kind = "AGENT"
	}
	active, err := m.commentMembers.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return "", err
	}
	if !active {
		return "", ErrForbidden
	}
	c, err := m.policy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return "", err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return "", ErrForbidden
	}
	return kind, nil
}

type ArtifactCommentCreate struct {
	AnchorKind      string                `json:"anchor_kind,omitempty" enum:"CODE_LINES,TABLE_ROW,TEXT_PARAGRAPH,HTML_REGION,MEDIA_TIME"`
	ViewerKind      string                `json:"viewer_kind,omitempty" enum:"CODE,DOCUMENT,WEB_DEVICE,REPORT"`
	RowIndex        *int                  `json:"row_index,omitempty" minimum:"0"`
	RowSHA256       string                `json:"row_sha256,omitempty" pattern:"^[a-f0-9]{64}$"`
	ParagraphIndex  *int                  `json:"paragraph_index,omitempty" minimum:"0"`
	ParagraphSHA256 string                `json:"paragraph_sha256,omitempty" pattern:"^[a-f0-9]{64}$"`
	TextQuote       string                `json:"text_quote,omitempty" maxLength:"500"`
	Selector        string                `json:"selector,omitempty" maxLength:"512"`
	Rect            *ClientAnchorRect     `json:"rect,omitempty"`
	Viewport        *ClientAnchorViewport `json:"viewport,omitempty"`
	Scroll          *ClientAnchorPoint    `json:"scroll,omitempty"`
	SeenSeq         *int64                `json:"seen_seq,omitempty" minimum:"0"`
	EvidenceID      guid.ID               `json:"evidence_id"`
	StartLine       int                   `json:"start_line" minimum:"1"`
	EndLine         int                   `json:"end_line" minimum:"1"`
	Body            string                `json:"body" minLength:"1" maxLength:"8000"`
}
type ArtifactCommentView struct {
	CreatedBy       guid.ID                          `json:"created_by"`
	ExecutorID      guid.ID                          `json:"executor_id,omitempty"`
	FirstRetracted  bool                             `json:"first_retracted"`
	FeedbackID      guid.ID                          `json:"feedback_id,omitempty"`
	AnchorStatus    string                           `json:"anchor_status"`
	RelocatedAnchor *delivery.ArtifactAnchorSnapshot `json:"relocated_anchor,omitempty"`
	LastSeq         int64                            `json:"last_seq"`
	ID              guid.ID                          `json:"id"`
	ExecutionID     guid.ID                          `json:"execution_id"`
	WorkItemID      guid.ID                          `json:"work_item_id"`
	EvidenceID      guid.ID                          `json:"evidence_id"`
	Visibility      string                           `json:"visibility"`
	State           string                           `json:"state"`
	Anchor          delivery.ArtifactAnchorSnapshot  `json:"anchor"`
	FirstBody       string                           `json:"first_body"`
	DefectID        guid.ID                          `json:"defect_id,omitempty"`
	CreatedAt       time.Time                        `json:"created_at"`
}

func artifactCodeAnchor(pin files.PinnedResourceRead, fact ArtifactFact, start, end int) (delivery.ArtifactAnchorSnapshot, error) {
	var out delivery.ArtifactAnchorSnapshot
	if pin.Reader == nil {
		return out, ErrUnavailable
	}
	data, readErr := io.ReadAll(io.LimitReader(pin.Reader, 65537))
	if err := errors.Join(readErr, pin.Reader.Close()); err != nil {
		return out, err
	}
	if len(data) > 65536 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || hash(data) != fact.SHA256 || pin.SHA256 != fact.SHA256 || pin.BindingID != fact.BindingID || pin.FileID != fact.FileID || pin.VersionID != fact.VersionID {
		return out, ErrInvalidArgument
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if start < 1 || end < start || end > len(lines) || end-start >= 100 {
		return out, ErrInvalidArgument
	}
	excerpt := strings.Join(lines[start-1:end], "\n")
	if len(excerpt) > 12000 {
		return out, ErrInvalidArgument
	}
	return delivery.ArtifactAnchorSnapshot{Kind: "CODE_LINES", Path: pin.Name, VersionRef: fmt.Sprintf("file-version:%s sha256:%s", fact.VersionID.String(), fact.SHA256), FileID: fact.FileID, VersionID: fact.VersionID, SHA256: fact.SHA256, StartLine: start, EndLine: end, Excerpt: excerpt}, nil
}

func (m *Module) CreateArtifactCommentOn(ctx context.Context, tx *gorm.DB, tenant, execution guid.ID, in ArtifactCommentCreate) (ArtifactCommentView, error) {
	var out ArtifactCommentView
	if err := transaction(tx, tenant, execution); err != nil {
		return out, err
	}
	if !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || utf8.RuneCountInString(in.Body) > 8000 || in.EvidenceID <= 0 {
		return out, ErrInvalidArgument
	}
	row, err := m.rowOn(ctx, tx, tenant, execution, false)
	if err != nil {
		return out, err
	}
	kind, err := m.commentAuthority(ctx, tx, tenant, row.ProjectID, "artifact_comment_write")
	if err != nil {
		return out, err
	}
	if err = m.checkExecutionContextOn(ctx, tx, row, in.SeenSeq, kind == "AGENT"); err != nil {
		return out, err
	}
	// Serialize the execution-wide cap, including historical ADHOC executions.
	if _, err = m.rowOn(ctx, tx, tenant, execution, true); err != nil {
		return out, err
	}
	var existingThreads []model.ArtifactCommentThread
	if err = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND execution_id = ?", tenant, execution).Limit(2000).Find(&existingThreads).Error; err != nil {
		return out, err
	}
	if len(existingThreads) >= 2000 {
		return out, ErrCommentLimit
	}
	if m.commentFiles == nil || m.commentActivities.RecordOn == nil {
		return out, ErrUnavailable
	}
	q := query.BindEvidence(tx, m.names)
	evidence, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ExecutionID.Eq(execution), q.ProjectID.Eq(row.ProjectID), q.ID.Eq(in.EvidenceID), q.Kind.Eq("FILE")).First()
	if err != nil {
		return out, err
	}
	var fact ArtifactFact
	if json.Unmarshal([]byte(evidence.Content), &fact) != nil || fact.BindingID <= 0 || fact.FileID <= 0 || fact.VersionID <= 0 {
		return out, ErrUnavailable
	}
	readCtx := context.WithValue(ctx, artifactCommentReadKey{}, artifactCommentRead{tenant, row.ProjectID, evidence.ID})
	pin, err := m.commentFiles.OpenOn(readCtx, tx, fact.BindingID)
	if err != nil {
		return out, err
	}
	if pin.ResourceID != evidence.ID || pin.Purpose != "execution_evidence" {
		if pin.Reader != nil {
			return out, errors.Join(ErrForbidden, pin.Reader.Close())
		}
		return out, ErrForbidden
	}
	anchor, err := internalArtifactAnchor(pin, fact, in)
	if err != nil {
		return out, err
	}
	encoded, err := json.Marshal(anchor)
	if err != nil {
		return out, err
	}
	id, err := m.nextID()
	if err != nil {
		return out, err
	}
	mid, err := m.nextID()
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)
	thread := model.ArtifactCommentThread{ID: id, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: execution, WorkItemID: row.WorkItemID, EvidenceID: evidence.ID, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: string(encoded), FirstBody: in.Body, CreatedBy: actor, CreatedAt: now, Version: 1}
	// The activity recorder locks the source work before the thread domain rows.
	if row.WorkItemID > 0 {
		if _, err = m.commentActivities.RecordOn(ctx, tx, tenant, row.WorkItemID, delivery.WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: kind, ActorID: actor, RefKind: "artifact_comment", RefID: id, Title: "Artifact comment added"}); err != nil {
			return out, err
		}
	}
	if err = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Create(&thread).Error; err != nil {
		return out, err
	}
	message := model.ArtifactCommentMessage{ActorKind: kind, Seq: 1, ID: mid, TenantID: tenant, ThreadID: id, Body: in.Body, CreatedBy: actor, CreatedAt: now}
	if err = tx.WithContext(ctx).Table(m.names.CommentMessages.String()).Create(&message).Error; err != nil {
		return out, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.artifact_comment_added", kind, "permission:artifact_comment_write", map[string]any{"thread_id": id.String(), "evidence_id": evidence.ID.String(), "kind": "COMMENT_ADDED"}); err != nil {
		return out, err
	}
	return ArtifactCommentView{AnchorStatus: "CURRENT", CreatedBy: actor, LastSeq: 1, ID: id, ExecutionID: execution, WorkItemID: row.WorkItemID, EvidenceID: evidence.ID, Visibility: thread.Visibility, State: thread.State, Anchor: anchor, FirstBody: in.Body, CreatedAt: now}, nil
}

func (m *Module) artifactCommentScopeOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (delivery.ArtifactCommentScope, error) {
	var out delivery.ArtifactCommentScope
	if err := transaction(tx, tenant, id); err != nil {
		return out, err
	}
	var row model.ArtifactCommentThread
	err := tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Select("project_id", "work_item_id").Where("tenant_id = ? AND id = ?", tenant, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	return delivery.ArtifactCommentScope{ProjectID: row.ProjectID, WorkItemID: row.WorkItemID}, err
}
func (m *Module) artifactCommentSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (delivery.ArtifactCommentSource, error) {
	var out delivery.ArtifactCommentSource
	if err := transaction(tx, tenant, id); err != nil {
		return out, err
	}
	var row model.ArtifactCommentThread
	err := tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Where("tenant_id = ? AND id = ?", tenant, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if _, err = m.commentAuthority(ctx, tx, tenant, row.ProjectID, "execution_read"); err != nil {
		return out, err
	}
	out = delivery.ArtifactCommentSource{ID: row.ID, TenantID: tenant, ProjectID: row.ProjectID, ExecutionID: row.ExecutionID, WorkItemID: row.WorkItemID, Visibility: row.Visibility, State: row.State, FirstBody: row.FirstBody}
	if json.Unmarshal([]byte(row.AnchorSnapshot), &out.Anchor) != nil {
		return out, ErrUnavailable
	}
	var conversion model.ArtifactCommentConversion
	err = tx.WithContext(ctx).Table(m.names.CommentConversions.String()).Where("tenant_id = ? AND thread_id = ? AND target_kind = ?", tenant, id, "DEFECT").First(&conversion).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	out.DefectID = conversion.TargetID
	conversion = model.ArtifactCommentConversion{}
	err = tx.WithContext(ctx).Table(m.names.CommentConversions.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND thread_id = ? AND target_kind = ?", tenant, id, "FEEDBACK").Limit(1).Find(&conversion).Error
	if err != nil {
		return out, err
	}
	out.FeedbackID = conversion.TargetID
	return out, nil
}
func (m *Module) markArtifactCommentConvertedOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, kind string, target, actor guid.ID) error {
	if kind != "DEFECT" && kind != "FEEDBACK" {
		return ErrInvalidArgument
	}
	if target <= 0 || actor != authn.PrincipalIDFromContext(ctx) {
		return ErrInvalidArgument
	}
	source, err := m.artifactCommentSourceOn(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	if source.Visibility != "INTERNAL" {
		return ErrForbidden
	}
	var row model.ArtifactCommentThread
	if err = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND id = ?", tenant, id).First(&row).Error; err != nil {
		return err
	}
	cid, err := m.nextID()
	if err != nil {
		return err
	}
	actorKind, err := m.commentActorKindOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	receipt := model.ArtifactCommentConversion{ActorKind: actorKind, Seq: int64(row.Version) + 1, ID: cid, TenantID: tenant, ThreadID: id, TargetKind: kind, TargetID: target, ActorID: actor, CreatedAt: time.Now().UTC()}
	if err = tx.WithContext(ctx).Table(m.names.CommentConversions.String()).Create(&receipt).Error; err != nil {
		return err
	}
	if err = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Where("tenant_id = ? AND id = ?", tenant, id).Updates(map[string]any{"state": "RESOLVED", "version": row.Version + 1}).Error; err != nil {
		return err
	}
	execution, err := m.rowOn(ctx, tx, tenant, row.ExecutionID, false)
	if err != nil {
		return err
	}
	return m.eventOn(ctx, tx, execution, "execution.comment_converted", actorKind, "capability:delivery.comment_conversion", map[string]any{"thread_id": id.String(), "target_kind": kind, "target_id": target.String(), "thread_seq": receipt.Seq})
}
