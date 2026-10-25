package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"gorm.io/gorm"
)

// ArtifactAnchorSnapshot contains source-owned immutable content identity and
// excerpt. Callers cannot assert a commit or replace this with live file bytes.
type ArtifactAnchorSnapshot struct {
	Locator    json.RawMessage `json:"locator,omitempty"`
	Kind       string          `json:"kind"`
	Path       string          `json:"path"`
	VersionRef string          `json:"version_ref"`
	FileID     guid.ID         `json:"file_id"`
	VersionID  guid.ID         `json:"version_id"`
	SHA256     string          `json:"sha256"`
	StartLine  int             `json:"start_line"`
	EndLine    int             `json:"end_line"`
	Excerpt    string          `json:"excerpt"`
}
type ArtifactCommentSource struct {
	ID, TenantID, ProjectID, ExecutionID, WorkItemID guid.ID
	Visibility, State, FirstBody                     string
	Anchor                                           ArtifactAnchorSnapshot
	DefectID                                         guid.ID
	FeedbackID                                       guid.ID
}

// ArtifactCommentsConnector lets the source owner bind its real Get/convert
// commands without introducing a delivery -> execution dependency cycle.
type ArtifactComments struct {
	ScopeOn         func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentScope, error)
	GetOn           func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentSource, error)
	MarkConvertedOn func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, guid.ID) error
}

// Scope carries no comment body or artifact content. It is used to resolve the
// resource owner and lock principals before current IAM and business rows.
type ArtifactCommentScope struct{ ProjectID, WorkItemID guid.ID }
type ArtifactCommentsConnector struct {
	Bind func(ArtifactComments) error
	// FeedbackOn returns authorized, frozen canonical feedback for the current result.
	FeedbackOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (string, error)
}

func (m *Module) provideArtifactComments(s *module.Services) error {
	return module.Provide[ArtifactCommentsConnector](s, ArtifactCommentsConnector{FeedbackOn: m.commentFeedbackSnapshotOn, Bind: func(source ArtifactComments) error {
		if source.ScopeOn == nil || source.GetOn == nil || source.MarkConvertedOn == nil || m.artifactComments.GetOn != nil {
			return ErrUnavailable
		}
		m.artifactComments = source
		return nil
	}})
}

// commentFeedbackSnapshotOn preserves canonical provenance while bounding runtime
// context. The count makes omitted older feedback explicit; stored feedback is intact.
func (m *Module) commentFeedbackSnapshotOn(ctx context.Context, tx *gorm.DB, tenant, project, work, actor guid.ID) (string, error) {
	current, err := m.readWork(ctx, tx, tenant, project, work, actor)
	if err != nil {
		return "", err
	}
	if current.CurrentClaimID == nil {
		return "", nil
	}
	q := query.BindResultFeedback(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(work), q.ResultVersionID.Eq(*current.CurrentClaimID), q.Kind.Eq("FEEDBACK"), q.CommentThreadID.IsNotNull())
	total, err := base.Count()
	if err != nil || total == 0 {
		return "", err
	}
	rows, err := base.Order(q.CreatedAt.Desc(), q.ID.Desc()).Limit(100).Find()
	if err != nil {
		return "", err
	}
	snapshot := struct {
		Feedback []resultFeedbackView `json:"feedback"`
		Omitted  int64                `json:"omitted_count"`
	}{Feedback: []resultFeedbackView{}, Omitted: total}
	for _, row := range rows {
		item := feedbackRecord(row)
		if item.AnchorSnapshot != nil && len(item.AnchorSnapshot.Excerpt) > 1200 {
			item.AnchorSnapshot.Excerpt = shortComment(item.AnchorSnapshot.Excerpt, 300) + " [excerpt truncated; pinned version identifies full source]"
		}
		snapshot.Feedback = append(snapshot.Feedback, item)
		snapshot.Omitted--
		encoded, e := json.Marshal(snapshot)
		if e != nil {
			return "", e
		}
		if len(encoded) > 15900 {
			// Budget fallback retains IDs, pinned version and original path/line provenance.
			item.Detail, item.Expected = "", ""
			item.Location = shortComment(item.Location, 200)
			if item.AnchorSnapshot != nil {
				item.AnchorSnapshot.Excerpt = ""
				item.AnchorSnapshot.Locator = nil
			}
			snapshot.Feedback[len(snapshot.Feedback)-1] = item
			encoded, e = json.Marshal(snapshot)
			if e != nil {
				return "", e
			}
			if len(encoded) > 15900 {
				snapshot.Feedback = snapshot.Feedback[:len(snapshot.Feedback)-1]
				snapshot.Omitted++
				break
			}
		}
	}
	encoded, err := json.Marshal(snapshot)
	return string(encoded), err
}
