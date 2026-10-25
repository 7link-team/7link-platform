package execution

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func internalArtifactAnchor(pin files.PinnedResourceRead, fact ArtifactFact, in ArtifactCommentCreate) (delivery.ArtifactAnchorSnapshot, error) {
	if in.AnchorKind == "" || in.AnchorKind == "CODE_LINES" {
		if in.ViewerKind != "" && in.ViewerKind != "CODE" && in.ViewerKind != "DOCUMENT" {
			if pin.Reader != nil {
				return delivery.ArtifactAnchorSnapshot{}, errors.Join(ErrInvalidArgument, pin.Reader.Close())
			}
			return delivery.ArtifactAnchorSnapshot{}, ErrInvalidArgument
		}
		return artifactCodeAnchor(pin, fact, in.StartLine, in.EndLine)
	}
	anchor, err := clientArtifactAnchor(pin, ClientDeliveryItem{VersionID: fact.VersionID, SHA256: fact.SHA256}, ClientThreadCreate{AnchorKind: in.AnchorKind, ViewerKind: in.ViewerKind, RowIndex: in.RowIndex, RowSHA256: in.RowSHA256, ParagraphIndex: in.ParagraphIndex, ParagraphSHA256: in.ParagraphSHA256, TextQuote: in.TextQuote, Selector: in.Selector, Rect: in.Rect, Viewport: in.Viewport, Scroll: in.Scroll})
	anchor.ArtifactAnchorSnapshot.Locator = anchor.Anchor
	return anchor.ArtifactAnchorSnapshot, err
}

type ArtifactCommentMutation struct {
	SeenSeq    *int64  `json:"seen_seq,omitempty" minimum:"0"`
	Body       string  `json:"body,omitempty" maxLength:"8000"`
	State      string  `json:"state,omitempty" enum:"OPEN,RESOLVED"`
	EvidenceID guid.ID `json:"evidence_id,omitempty"`
}

// lockCommentMutationOn preserves the established principal/project/work/thread
// order used by replies and conversions. Client threads remain read-only here.
func (m *Module) lockCommentMutationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, seen *int64) (model.ArtifactCommentThread, *model.Execution, string, error) {
	var empty model.ArtifactCommentThread
	if err := transaction(tx, tenant, id); err != nil {
		return empty, nil, "", err
	}
	thread, err := m.commentThreadOn(ctx, tx, tenant, id, false)
	if err != nil {
		return empty, nil, "", err
	}
	kind, err := m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "execution_read")
	if err != nil {
		return empty, nil, "", err
	}
	row, err := m.rowOn(ctx, tx, tenant, thread.ExecutionID, false)
	if err != nil {
		return empty, nil, "", err
	}
	if err = m.checkExecutionContextOn(ctx, tx, row, nil, false); err != nil {
		return empty, nil, "", err
	}
	thread, err = m.commentThreadOn(ctx, tx, tenant, id, true)
	if err != nil {
		return empty, nil, "", err
	}
	if thread.Visibility != "INTERNAL" {
		return empty, nil, "", ErrClientThreadReadOnly
	}
	if err = m.checkCommentThreadSeenOn(ctx, tx, thread, seen, kind == "AGENT"); err != nil {
		return empty, nil, "", err
	}
	if thread.Version == math.MaxInt32 {
		return empty, nil, "", ErrConflict
	}
	return thread, row, kind, nil
}

func (m *Module) appendCommentLifecycleOn(ctx context.Context, tx *gorm.DB, thread model.ArtifactCommentThread, row *model.Execution, kind, event string, updates map[string]any) error {
	id, err := m.nextID()
	if err != nil {
		return err
	}
	seq := int64(thread.Version) + 1
	record := model.ArtifactCommentEvent{ID: id, TenantID: thread.TenantID, ThreadID: thread.ID, Seq: seq, Kind: event, ActorID: authn.PrincipalIDFromContext(ctx), ActorKind: kind, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err = tx.WithContext(ctx).Table(m.names.CommentEvents.String()).Create(&record).Error; err != nil {
		return err
	}
	updates["version"] = thread.Version + 1
	if err = tx.WithContext(ctx).Table(m.names.CommentThreads.String()).Where("tenant_id = ? AND id = ?", thread.TenantID, thread.ID).Updates(updates).Error; err != nil {
		return err
	}
	if err = m.recordExecutionActivityOn(ctx, tx, row, "STATE_CHANGED", "artifact_comment", thread.ID, kind); err != nil {
		return err
	}
	return m.eventOn(ctx, tx, row, "execution."+event, kind, "permission:execution_read", map[string]any{"thread_id": thread.ID.String(), "thread_seq": seq})
}

func (m *Module) ChangeArtifactCommentStateOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, expected int64, in ArtifactCommentMutation) (ArtifactCommentMessages, error) {
	var empty ArtifactCommentMessages
	thread, row, kind, err := m.lockCommentMutationOn(ctx, tx, tenant, id, in.SeenSeq)
	if err != nil {
		return empty, err
	}
	if expected != int64(thread.Version) {
		return empty, ErrConflict
	}
	if in.State != "OPEN" && in.State != "RESOLVED" {
		return empty, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor != thread.CreatedBy && !commentExecutor(row, actor) {
		if _, err = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "work_item_write"); err != nil {
			return empty, err
		}
	}
	if thread.State == in.State {
		return ArtifactCommentMessages{Items: []ArtifactCommentMessageView{}, LastSeq: int64(thread.Version)}, nil
	}
	event := "comment_thread_resolved"
	if in.State == "OPEN" {
		event = "comment_thread_reopened"
	}
	if err = m.appendCommentLifecycleOn(ctx, tx, thread, row, kind, event, map[string]any{"state": in.State}); err != nil {
		return empty, err
	}
	return ArtifactCommentMessages{Items: []ArtifactCommentMessageView{}, LastSeq: int64(thread.Version) + 1}, nil
}

func (m *Module) MutateArtifactCommentMessageOn(ctx context.Context, tx *gorm.DB, tenant, threadID, messageID guid.ID, expected int64, retract bool, in ArtifactCommentMutation) (ArtifactCommentMessageView, error) {
	var empty ArtifactCommentMessageView
	thread, row, kind, err := m.lockCommentMutationOn(ctx, tx, tenant, threadID, in.SeenSeq)
	if err != nil {
		return empty, err
	}
	if expected != int64(thread.Version) {
		return empty, ErrConflict
	}
	var message model.ArtifactCommentMessage
	if err = tx.WithContext(ctx).Table(m.names.CommentMessages.String()).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND thread_id = ? AND id = ?", tenant, threadID, messageID).Take(&message).Error; err != nil {
		return empty, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor != message.CreatedBy {
		if !retract {
			return empty, ErrForbidden
		}
		if _, err = m.commentAuthority(ctx, tx, tenant, thread.ProjectID, "work_item_write"); err != nil {
			return empty, err
		}
	}
	if message.RetractedAt != nil {
		return empty, ErrConflict
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	updates := map[string]any{}
	event := "comment_edited"
	if retract {
		message.RetractedAt = &now
		message.Body = ""
		updates["retracted_at"], updates["body"] = now, ""
		event = "comment_retracted"
	} else {
		if now.After(message.CreatedAt.Add(15*time.Minute)) || message.EditCount >= 10 {
			return empty, ErrConflict
		}
		if !utf8.ValidString(in.Body) || strings.TrimSpace(in.Body) == "" || utf8.RuneCountInString(in.Body) > 8000 {
			return empty, ErrInvalidArgument
		}
		message.Body, message.EditCount, message.EditedAt = in.Body, message.EditCount+1, &now
		updates["body"], updates["edit_count"], updates["edited_at"] = in.Body, message.EditCount, now
	}
	if err = tx.WithContext(ctx).Table(m.names.CommentMessages.String()).Where("tenant_id = ? AND id = ?", tenant, messageID).Updates(updates).Error; err != nil {
		return empty, err
	}
	if err = m.appendCommentLifecycleOn(ctx, tx, thread, row, kind, event, map[string]any{}); err != nil {
		return empty, err
	}
	return commentMessageView(message), nil
}

func decodeRelocatedAnchor(thread model.ArtifactCommentThread, out *ArtifactCommentView) error {
	out.AnchorStatus = thread.AnchorStatus
	if out.AnchorStatus == "" {
		out.AnchorStatus = "CURRENT"
	}
	if thread.RelocatedAnchor != nil {
		var anchor delivery.ArtifactAnchorSnapshot
		if json.Unmarshal([]byte(*thread.RelocatedAnchor), &anchor) != nil {
			return ErrUnavailable
		}
		out.RelocatedAnchor = &anchor
	}
	return nil
}

func commentExecutor(row *model.Execution, actor guid.ID) bool {
	var plan ExecutionPlan
	return json.Unmarshal([]byte(row.PlanJSON), &plan) == nil && plan.ExecutorPrincipalID == actor
}
