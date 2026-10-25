package templates

import (
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestVideoHTTPReplayAndCurrentAuthorization(t *testing.T) {
	m, _, ctx := bindStudio(t, "VIDEO")
	in := &videoCreateInput{ProjectID: "99", IdempotencyKey: "video-once", Body: videoShot()}
	first, e := m.videoCreateHTTP(ctx, in)
	require.NoError(t, e)
	second, e := m.videoCreateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.ID, second.Body.Data.ID)
	board, e := m.videoReadHTTP(ctx, &novelInput{ProjectID: "99"})
	require.NoError(t, e)
	require.Len(t, board.Body.Data.Items, 1)
	require.Equal(t, "UNAVAILABLE", board.Body.Data.Availability)
	in.Body.Content.Title = "Changed"
	_, e = m.videoCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.Body = videoShot()
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.videoCreateHTTP(ctx, in)
	assertStatus(t, e, 403)
	_, e = m.videoReadHTTP(ctx, &novelInput{ProjectID: "99"})
	assertStatus(t, e, 403)
	_, e = m.videoContentHTTP(ctx, &videoContentInput{ProjectID: "bad", ID: "1"})
	assertStatus(t, e, 422)
}

func TestVideoHTTPNullableShotSourceIDsWire(t *testing.T) {
	m, _, ctx := bindStudio(t, "VIDEO")
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "video-wire", Method: "POST", Path: "/projects/{project_id}/video-workbench", DefaultStatus: 201}, func(_ context.Context, in *videoCreateInput) (*respx.Body[StudioRecordView], error) {
		return m.videoCreateHTTP(ctx, in)
	})
	raw, err := json.Marshal(videoShot())
	require.NoError(t, err)
	url := "/projects/99/video-workbench"
	for _, field := range []string{"parent_id", "execution_id", "generation_id", "evidence_id"} {
		require.Contains(t, string(raw), `"`+field+`":null`)
		for _, invalid := range []string{`"0"`, `""`} {
			bad := strings.Replace(string(raw), `"`+field+`":null`, `"`+field+`":`+invalid, 1)
			require.Equal(t, 422, api.Post(url, "Idempotency-Key: bad", strings.NewReader(bad)).Code, field)
		}
	}
	response := api.Post(url, "Idempotency-Key: actual-shot", strings.NewReader(string(raw)))
	require.Equal(t, 201, response.Code, response.Body.String())
}

func TestVideoHTTPStreamsPinnedMediaAndReauthorizesEdits(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	artifact := VideoArtifact{EvidenceID: 500, BindingID: 501, FileID: 502, VersionID: 503, SHA256: strings.Repeat("a", 64)}
	m.videoSource = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error) {
		return VideoExecution{Artifacts: []VideoArtifact{artifact}}, nil
	}
	in := videoShot()
	in.Content.Video.Role = "REFERENCE"
	in.Content.Video.ExecutionID = 123
	in.Content.Video.EvidenceID = 500
	row := saveVideo(t, m, db, ctx, 0, in)
	f := &videoFileFixture{resource: files.PinnedResource{BindingID: 501, FileID: 502, VersionID: 503, SHA256: artifact.SHA256, MIME: "video/mp4"}}
	m.evalFiles = f
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "media-stream", Method: "GET", Path: "/media"}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return m.videoContentHTTP(ctx, &videoContentInput{ProjectID: "99", ID: row.ID.String()})
	})
	response := api.Get("/media")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "actual immutable fixture bytes", response.Body.String())
	require.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, `"`+artifact.SHA256+`"`, response.Header().Get("ETag"))
	require.True(t, f.closed)
	in.Version = row.Version
	in.Content.Title = "Updated reference"
	edit := &videoEditInput{ProjectID: "99", ID: row.ID.String(), IdempotencyKey: "edit-reference", Body: in}
	updated, e := m.videoEditHTTP(ctx, edit)
	require.NoError(t, e)
	require.Equal(t, "Updated reference", updated.Body.Data.Content.Title)
	m.videoSource = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error) {
		return VideoExecution{}, ErrForbidden
	}
	_, e = m.videoEditHTTP(ctx, edit)
	assertStatus(t, e, 403)
	_, e = m.videoContentHTTP(ctx, &videoContentInput{ProjectID: "99", ID: row.ID.String()})
	assertStatus(t, e, 403)
	for _, test := range []func() error{
		func() error { _, e := m.videoReadHTTP(ctx, &novelInput{ProjectID: "bad"}); return e },
		func() error { _, e := m.videoCreateHTTP(ctx, &videoCreateInput{ProjectID: "bad"}); return e },
		func() error { _, e := m.videoEditHTTP(ctx, &videoEditInput{ProjectID: "bad", ID: "1"}); return e },
		func() error { _, e := m.videoEditHTTP(ctx, &videoEditInput{ProjectID: "99", ID: "bad"}); return e },
		func() error {
			_, e := m.videoContentHTTP(ctx, &videoContentInput{ProjectID: "99", ID: "bad"})
			return e
		},
	} {
		assertStatus(t, test(), 422)
	}
	_, e = m.videoCreateHTTP(ctx, &videoCreateInput{ProjectID: "99", IdempotencyKey: "generate-without-source", Body: VideoEdit{Action: "GENERATE", Reason: "Request"}})
	assertStatus(t, e, 503)
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrForbidden }
	generation := videoShot()
	generation.Action = "GENERATE"
	generation.Content.Video.ExecutionID = 0
	_, e = m.videoCreateHTTP(ctx, &videoCreateInput{ProjectID: "99", IdempotencyKey: "generation-denied", Body: generation})
	assertStatus(t, e, 403)
}
