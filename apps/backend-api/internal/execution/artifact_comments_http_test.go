package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestArtifactCommentListPaginatesFrozenInternalThreadsOnly(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	for i, visibility := range []string{"INTERNAL", "CLIENT", "INTERNAL"} {
		r := model.ArtifactCommentThread{ID: guid.ID(900 + i), TenantID: 7, ProjectID: 9, ExecutionID: v.ID, WorkItemID: validCreate().WorkItemID, EvidenceID: 950, Visibility: visibility, State: "OPEN", AnchorSnapshot: `{"kind":"CODE_LINES","version_id":"3","excerpt":"frozen lines"}`, FirstBody: "Original comment", CreatedBy: 11, CreatedAt: time.Now().UTC(), Version: 1}
		require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Create(&r).Error)
	}
	first, err := f.m.listArtifactCommentsHTTP(f.human, &artifactCommentListInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Body.Data.Items, 1)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, "900", first.Body.Data.NextCursor)
	second, err := f.m.listArtifactCommentsHTTP(f.human, &artifactCommentListInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, Cursor: first.Body.Data.NextCursor, Limit: 1})
	require.NoError(t, err)
	require.Len(t, second.Body.Data.Items, 1)
	require.EqualValues(t, 902, second.Body.Data.Items[0].ID)
	require.False(t, second.Body.Data.HasMore)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "artifact-comment-input", Method: "POST", Path: artifactCommentsPath}, func(_ context.Context, in *artifactCommentInput) (*struct{ Body bool }, error) {
		return &struct{ Body bool }{true}, nil
	})
	for _, body := range []string{`{"evidence_id":"1","start_line":0,"end_line":1,"body":"Issue"}`, `{"evidence_id":"1","start_line":1,"end_line":1,"body":""}`} {
		require.Equal(t, 422, api.Post("/executions/1/artifact-comments", "Idempotency-Key: contract", strings.NewReader(body)).Code)
	}
}

func TestArtifactCommentsHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("createArtifactCommentHTTP"+id, func(t *testing.T) {
			_, err := f.m.createArtifactCommentHTTP(f.human, &artifactCommentInput{ExecutionInput: ExecutionInput{ExecutionID: id}, IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("listArtifactCommentsHTTP"+id, func(t *testing.T) {
			_, err := f.m.listArtifactCommentsHTTP(f.human, &artifactCommentListInput{ExecutionInput: ExecutionInput{ExecutionID: id}, Limit: 10})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestArtifactCommentHTTPStorageFailuresPreserveExactPinnedAnchor(t *testing.T) {
	for _, command := range []string{"create", "list", "source"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, id, evidence := pinnedCommentFixture(t)
				in := &artifactCommentInput{ExecutionInput: ExecutionInput{ExecutionID: id.String()}, IdempotencyKey: "pinned-comment", Body: ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review exact source"}}
				if command == "list" {
					_, e := f.m.createArtifactCommentHTTP(f.human, in)
					require.NoError(t, e)
				}
				return f, func(*gorm.DB) error {
					switch command {
					case "create":
						out, e := f.m.createArtifactCommentHTTP(f.human, in)
						if e == nil {
							require.Equal(t, evidence, out.Body.Data.EvidenceID)
						}
						return e
					case "list":
						out, e := f.m.listArtifactCommentsHTTP(f.human, &artifactCommentListInput{ExecutionInput: in.ExecutionInput, Limit: 1})
						if e == nil {
							require.Len(t, out.Body.Data.Items, 1)
						}
						return e
					default:
						out, e := f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: in.ExecutionInput, EvidenceID: evidence.String()})
						if e == nil {
							require.NotEmpty(t, out.Body.Data.Text)
						}
						return e
					}
				}
			}, true)
		})
	}
}

func TestArtifactCommentExecutionOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.artifactCommentExecutionOwner(f.human, 999)
		require.Error(t, e)
		require.False(t, found)
	}
}
