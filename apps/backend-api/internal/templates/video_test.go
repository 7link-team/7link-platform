package templates

import (
	"7link.com/platform/internal/files"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func videoShot() VideoEdit {
	return VideoEdit{Action: "SAVE", Reason: "Author storyboard", Content: StudioContent{Title: "Opening", Text: "Open gate", DurationMS: 3000, Video: &VideoData{Role: "SHOT", Character: "Lin", Framing: "Close", OutputIndex: 0}}}
}
func saveVideo(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID, in VideoEdit) StudioRecordView {
	t.Helper()
	var out StudioRecordView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.videoEditOn(ctx, tx, 99, id, in); return e }))
	return out
}
func TestVideoActualGenerationImportProvenanceAndCandidateSelection(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	health, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "video-health"})
	require.NoError(t, e)
	b, e := m.instanceOn(ctx, db, 99)
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.bindProvidersOn(ctx, tx, 99, b.Version, map[string]guid.ID{"VIDEO_GEN": provider.ID})
		return err
	}))
	require.Equal(t, "ENABLED", health.Body.Data.State)
	allow := true
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error {
		if !allow {
			return ErrForbidden
		}
		return nil
	}
	artifact := VideoArtifact{EvidenceID: 600, BindingID: 601, FileID: 602, VersionID: 603, SHA256: strings.Repeat("a", 64)}
	m.videoSource = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) (VideoExecution, error) {
		if !allow {
			return VideoExecution{}, ErrForbidden
		}
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(123), id)
		return VideoExecution{ExecutionID: id, WorkItemID: 90, State: "SUCCEEDED", Artifacts: []VideoArtifact{artifact}, QC: []VideoQC{}}, nil
	}
	shot := saveVideo(t, m, db, ctx, 0, videoShot())
	generate := videoShot()
	generate.Action = "GENERATE"
	generate.ParentID = shot.ID
	generate.Content.Video.ExecutionID = 123
	requested := saveVideo(t, m, db, ctx, 0, generate)
	generation := requested.Content.Video.Generation
	require.Equal(t, "PENDING", generation.State)
	require.NotZero(t, generation.ID)
	// Explicit durable provider fixture: terminal output is inserted as if the
	// existing trusted polling worker observed it, never as HTTP input.
	output := ProviderPoll{State: "COMPLETED", Outputs: []ProviderOutput{{FileRef: "file:602@603", SHA256: artifact.SHA256, Model: "fixture-model", ModelVersion: "2", Seed: "42"}}}
	raw, _ := json.Marshal(output)
	g := query.BindProviderGenerations(db, m.names)
	_, e = g.WithContext(ctx).Where(g.ID.Eq(generation.ID)).Updates(map[string]any{"state": "COMPLETED", "output_snapshot": string(raw), "output_sha256": evalDigest(raw)})
	require.NoError(t, e)
	imported := videoShot()
	imported.Action = "IMPORT"
	imported.ParentID = shot.ID
	imported.Content.Video = &VideoData{ExecutionID: 123, GenerationID: generation.ID}
	candidate := saveVideo(t, m, db, ctx, 0, imported)
	require.Equal(t, "CANDIDATE", candidate.Kind)
	require.Equal(t, artifact, *candidate.Content.Video.Artifact)
	require.Equal(t, "fixture", candidate.Content.Provider)
	require.Equal(t, "fixture-model/2", candidate.Content.Model)
	require.Equal(t, "42", candidate.Content.Seed)
	replayed := saveVideo(t, m, db, ctx, 0, imported)
	require.Equal(t, candidate.ID, replayed.ID)
	forged := imported
	copyContent := imported.Content
	copyVideo := *imported.Content.Video
	copyVideo.Generation = generation
	copyContent.Video = &copyVideo
	forged.Content = copyContent
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, 0, forged); return e }), ErrInvalid)
	selected := saveVideo(t, m, db, ctx, candidate.ID, VideoEdit{Action: "SELECT", Version: 1, Reason: "Human chooses attached output"})
	require.Equal(t, 2, selected.Version)
	row, e := m.studioRecordOn(ctx, db, shot.BindingID, shot.ID)
	require.NoError(t, e)
	require.Equal(t, &candidate.ID, row.SelectedID)
	allow = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.videoEditOn(ctx, tx, 99, candidate.ID, VideoEdit{Action: "SELECT", Version: 2, Reason: "Revoked"})
		return e
	}), ErrForbidden)
}
func TestVideoUnavailableCASCrossShotAndAtomicEffects(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	shot := saveVideo(t, m, db, ctx, 0, videoShot())
	var board VideoWorkbench
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; board, e = m.videoWorkbenchOn(ctx, tx, 99); return e }))
	require.Equal(t, "UNAVAILABLE", board.Availability)
	require.Empty(t, board.Executions)
	require.Empty(t, board.Providers)
	in := videoShot()
	in.Action = "GENERATE"
	in.ParentID = shot.ID
	in.Content.Video.ExecutionID = 123
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, 0, in); return e }), ErrUnavailable)
	in = videoShot()
	in.Version = 5
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, shot.ID, in); return e }), ErrConflict)
	in = videoShot()
	in.Content.Video.Role = "MASTER"
	in.Content.Video.ExecutionID = 123
	in.Content.Video.EvidenceID = 500
	m.videoSource = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error) {
		return VideoExecution{Artifacts: []VideoArtifact{{EvidenceID: 501}}}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, 0, in); return e }), ErrNotFound)
	q := query.BindStudioRecords(db, m.names)
	before, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
		return 0, errors.New("outbox unavailable")
	})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, 0, videoShot()); return e }))
	after, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, before, after)
}

type videoFileFixture struct {
	evalFileFixture
	resource       files.PinnedResource
	denied, closed bool
}
type videoReadCloser struct {
	io.Reader
	closed *bool
}

func (r videoReadCloser) Close() error { *r.closed = true; return nil }
func (f *videoFileFixture) OpenOn(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
	if f.denied {
		return files.PinnedResourceRead{}, files.ErrPinnedForbidden
	}
	if id != f.resource.BindingID {
		return files.PinnedResourceRead{}, files.ErrNotFound
	}
	f.closed = false
	return files.PinnedResourceRead{PinnedResource: f.resource, Reader: videoReadCloser{strings.NewReader("actual immutable fixture bytes"), &f.closed}}, nil
}
func TestVideoMediaReadPinsBindingVersionHashAndCurrentFilesAuthority(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	a := VideoArtifact{EvidenceID: 500, BindingID: 501, FileID: 502, VersionID: 503, SHA256: strings.Repeat("a", 64)}
	m.videoSource = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error) {
		return VideoExecution{Artifacts: []VideoArtifact{a}}, nil
	}
	in := videoShot()
	in.Content.Video.Role = "REFERENCE"
	in.Content.Video.ExecutionID = 123
	in.Content.Video.EvidenceID = 500
	row := saveVideo(t, m, db, ctx, 0, in)
	f := &videoFileFixture{resource: files.PinnedResource{BindingID: 501, FileID: 502, VersionID: 503, SHA256: a.SHA256, MIME: "video/mp4"}}
	m.evalFiles = f
	read := func() (out files.PinnedResourceRead, err error) {
		err = db.Transaction(func(tx *gorm.DB) error { out, err = m.videoOpenOn(ctx, tx, 99, row.ID); return err })
		return
	}
	resource, e := read()
	require.NoError(t, e)
	data, e := io.ReadAll(resource.Reader)
	require.NoError(t, e)
	require.Equal(t, "actual immutable fixture bytes", string(data))
	require.NoError(t, resource.Reader.Close())
	require.True(t, f.closed)
	f.resource.VersionID = 504
	_, e = read()
	require.ErrorIs(t, e, ErrConflict)
	require.True(t, f.closed)
	f.resource.VersionID = 503
	f.resource.MIME = "text/html"
	_, e = read()
	require.ErrorIs(t, e, ErrConflict)
	require.True(t, f.closed)
	f.resource.MIME = "video/mp4"
	f.denied = true
	_, e = read()
	require.ErrorIs(t, e, files.ErrPinnedForbidden)
}

func TestVideoWorkbenchStorageFailureAndUnavailableMedia(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	shot := saveVideo(t, m, db, ctx, 0, videoShot())
	injected := errors.New("video storage unavailable")
	for _, table := range []string{m.names.StudioRecords.String(), m.names.ProviderGenerations.String(), m.names.Providers.String(), m.names.ProviderBindings.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("video-read-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoWorkbenchOn(ctx, tx, 99); return e }), injected)
		require.NoError(t, db.Callback().Query().Remove("video-read-failure"))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoOpenOn(ctx, tx, 99, shot.ID); return e }), ErrUnavailable)
	m.evalFiles = &videoFileFixture{}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoOpenOn(ctx, tx, 99, shot.ID); return e }), ErrNotFound)
	for _, operation := range []string{"create", "update"} {
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == m.names.StudioRecords.String() {
				tx.AddError(injected)
			}
		}
		if operation == "create" {
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("video-write-failure", callback))
		} else {
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("video-write-failure", callback))
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			id := guid.ID(0)
			in := videoShot()
			if operation == "update" {
				id = shot.ID
				in.Version = 1
			}
			_, e := m.videoEditOn(ctx, tx, 99, id, in)
			return e
		}), injected)
		require.NoError(t, db.Callback().Create().Remove("video-write-failure"))
		require.NoError(t, db.Callback().Update().Remove("video-write-failure"))
	}
	for _, mutate := range []func(*VideoEdit){func(in *VideoEdit) { in.Reason = "" }, func(in *VideoEdit) { in.Content.Title = "" }, func(in *VideoEdit) { in.Content.Video.Role = "OTHER" }, func(in *VideoEdit) { in.Version = 1 }, func(in *VideoEdit) { in.Content.Video.Role = "REFERENCE" }} {
		in := videoShot()
		mutate(&in)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoEditOn(ctx, tx, 99, 0, in); return e }), ErrInvalid)
	}
	require.NoError(t, db.Table(m.names.StudioRecords.String()).Where("id=?", shot.ID).Update("payload", "invalid").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.videoWorkbenchOn(ctx, tx, 99); return e }))
}
