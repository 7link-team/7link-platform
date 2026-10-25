package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestArtifactCommentsConnectorRejectsIncompleteAndRepeatedBinding(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideArtifactComments(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[ArtifactCommentsConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(ArtifactComments{}), ErrUnavailable)
	source := ArtifactComments{ScopeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentScope, error) {
		return ArtifactCommentScope{}, nil
	}, GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (ArtifactCommentSource, error) {
		return ArtifactCommentSource{}, nil
	}, MarkConvertedOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID, guid.ID) error { return nil }}
	require.NoError(t, connector.Bind(source))
	require.ErrorIs(t, connector.Bind(source), ErrUnavailable)
}

func TestCommentFeedbackSnapshotUsesCurrentResultAndCurrentAuthority(t *testing.T) {
	m, db, ctx, in := feedbackHarness(t)
	work, err := guid.RequireID(in.WorkItemID)
	require.NoError(t, err)
	snapshot, err := m.commentFeedbackSnapshotOn(ctx, db, 1, 99, work, 11)
	require.NoError(t, err)
	require.Empty(t, snapshot)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("tenant_id = ? AND id = ?", 1, work).Update("current_claim_id", 801).Error)
	thread := guid.ID(930)
	anchor, err := json.Marshal(ArtifactAnchorSnapshot{Kind: "CODE_LINES", Path: "src/real.go", VersionRef: "file:70@71", FileID: 70, VersionID: 71, SHA256: strings.Repeat("a", 64), StartLine: 2, EndLine: 3, Excerpt: "frozen source"})
	require.NoError(t, err)
	frozen := string(anchor)
	for i := 0; i < 40; i++ {
		require.NoError(t, db.Table(m.service.names.ResultFeedback.String()).Create(&model.ResultFeedback{TenantID: 1, ID: guid.ID(940 + i), ProjectID: 99, WorkItemID: work, ResultVersionID: 801, Kind: "FEEDBACK", Category: "QUALITY_REJECTION", Detail: strings.Repeat("fix", 600), Location: "src/real.go:2-3", Expected: strings.Repeat("expected", 200), CommentThreadID: &thread, AnchorSnapshot: &frozen, ActorID: 11, CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}).Error)
		thread++
	}
	snapshot, err = m.commentFeedbackSnapshotOn(ctx, db, 1, 99, work, 11)
	require.NoError(t, err)
	require.LessOrEqual(t, len(snapshot), 16000)
	require.Contains(t, snapshot, "file:70@71")
	require.Contains(t, snapshot, "frozen source")
	var value struct {
		Feedback []resultFeedbackView `json:"feedback"`
		Omitted  int64                `json:"omitted_count"`
	}
	require.NoError(t, json.Unmarshal([]byte(snapshot), &value))
	require.NotEmpty(t, value.Feedback)
	require.Equal(t, int64(40), int64(len(value.Feedback))+value.Omitted)
	require.Positive(t, value.Omitted)
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("tenant_id = ? AND id = ?", 1, work).Update("current_claim_id", 802).Error)
	snapshot, err = m.commentFeedbackSnapshotOn(ctx, db, 1, 99, work, 11)
	require.NoError(t, err)
	require.Empty(t, snapshot)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("current access revoked")
	}
	_, err = m.commentFeedbackSnapshotOn(ctx, db, 1, 99, work, 11)
	require.ErrorContains(t, err, "current access revoked")
}
