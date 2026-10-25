package execution

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"

	"7link.com/platform/internal/files"
	filesquery "7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func visualObservationFixture(t *testing.T) (*executionFixture, guid.ID, VerificationPlan, VerificationObservation) {
	t.Helper()
	f, in := verificationFixture(t)
	in.Method = "VISUAL"
	in.Argv = []string{"index.html", "320", "240"}
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, in); return e }))
	var plans []VerificationPlan
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		plans, e = f.m.pullVerificationOn(f.runner, tx, 7, f.fence)
		return e
	}))
	require.Len(t, plans, 1)
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	state := uint32(1)
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			img.Set(x, y, color.RGBA{R: byte(state), G: byte(state >> 8), B: byte(state >> 16), A: 255})
		}
	}
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, img))
	b, _ := json.Marshal(plans[0])
	now := time.Now().UTC()
	o := VerificationObservation{PlanHash: hash(b), HeadCommit: in.HeadCommit, BrowserProvider: "Chrome/test", Reason: "VISUAL_REVIEW_REQUIRED", StartedAt: now, EndedAt: now, Screenshots: []VisualScreenshot{{Name: "viewport-1-320x240.png", Data: data.Bytes(), SHA256: hash(data.Bytes()), Width: 320, Height: 240, Route: "index.html"}}}
	return f, id, plans[0], o
}

func TestVisualEvidenceAttachmentFailureRollsBackTaskAndPinnedFacts(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, id, p, o := visualObservationFixture(t)
		_, _, _, _ = bindRealArtifactFiles(t, f)
		prepared, e := f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
		require.NoError(t, e)
		return f, func(tx *gorm.DB) error {
			return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, p.Nonce, prepared)
		}
	})
	for _, fault := range []string{"missing", "criterion", "id", "attach", "digest"} {
		t.Run(fault, func(t *testing.T) {
			f, id, p, o := visualObservationFixture(t)
			_, _, _, _ = bindRealArtifactFiles(t, f)
			prepared, e := f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
			require.NoError(t, e)
			tasks := query.BindVerificationTasks(f.db, f.m.names)
			task, e := tasks.WithContext(t.Context()).Where(tasks.ID.Eq(id)).First()
			require.NoError(t, e)
			failure := errors.New("visual attachment failed")
			expected := failure
			switch fault {
			case "missing":
				f.m.artifacts.attach = nil
				expected = ErrCapabilityUnavailable
			case "criterion":
				p.CriterionID = "invalid"
				expected = ErrInvalidArgument
			case "id":
				f.m.ids = failedExecutionIDs{err: failure}
			case "attach":
				f.m.artifacts.attach = func(context.Context, *gorm.DB, *files.PreparedExecutionArtifact, guid.ID) (files.PinnedResource, error) {
					return files.PinnedResource{}, failure
				}
			case "digest":
				f.m.artifacts.attach = func(context.Context, *gorm.DB, *files.PreparedExecutionArtifact, guid.ID) (files.PinnedResource, error) {
					return files.PinnedResource{SHA256: "wrong"}, nil
				}
				expected = ErrConflict
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.attachVisualEvidenceOn(f.runner, tx, task, p, prepared) }), expected)
		})
	}
}

func TestVisualEvidenceRealStorageBytesReplayAndHonestVerdict(t *testing.T) {
	f, id, p, o := visualObservationFixture(t)
	require.Greater(t, len(o.Screenshots[0].Data), 65536, "exercise screenshot larger than MySQL TEXT and ordinary upload limits")
	_, _, _, driver := bindRealArtifactFiles(t, f)
	prepared, e := f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
	require.NoError(t, e)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, p.Nonce, prepared)
	}))
	tasks := query.BindVerificationTasks(f.db, f.m.names)
	task, e := tasks.WithContext(t.Context()).Where(tasks.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Less(t, len(task.ObservationJSON), 65536)
	var stored VerificationObservation
	require.NoError(t, json.Unmarshal([]byte(task.ObservationJSON), &stored))
	require.Empty(t, stored.Screenshots[0].Data)
	require.Equal(t, o.Screenshots[0].SHA256, stored.Screenshots[0].SHA256)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.verificationReceiptOn(f.human, tx, 7, 9, 3, 50, id)
		require.ErrorIs(t, e, ErrConflict, "delivery must keep review-required capture pending")
		return nil
	}))
	q := query.BindEvidence(f.db, f.m.names)
	r, e := q.WithContext(t.Context()).Where(q.Kind.Eq("FILE")).First()
	require.NoError(t, e)
	var fact ArtifactFact
	require.NoError(t, json.Unmarshal([]byte(r.Content), &fact))
	require.Equal(t, "PARTIAL", fact.Coverage)
	require.Equal(t, o.Screenshots[0].SHA256, fact.SHA256)
	fq := filesquery.Bind(f.db, "ex_")
	version, e := fq.Version.WithContext(t.Context()).Where(fq.Version.ID.Eq(fact.VersionID)).First()
	require.NoError(t, e)
	object, e := fq.Object.WithContext(t.Context()).Where(fq.Object.ID.Eq(version.ObjectID)).First()
	require.NoError(t, e)
	reader, e := driver.Open(t.Context(), object.StorageKey)
	require.NoError(t, e)
	data, e := io.ReadAll(reader)
	require.NoError(t, e)
	require.NoError(t, reader.Close())
	require.Equal(t, o.Screenshots[0].Data, data)
	before, e := fq.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, e)
	replayed, e := f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
	require.NoError(t, e)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, p.Nonce, replayed)
	}))
	after, e := fq.AccountPreparation.WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.Equal(t, before, after)
	o.Screenshots[0].Data = []byte("tampered")
	_, e = f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
	require.Error(t, e)
}

func TestVisualEvidenceRejectsForgedOrExpiredObservations(t *testing.T) {
	for _, fault := range []string{"hash", "dimensions", "route", "png", "nonce", "machine", "expired", "attach_failure"} {
		t.Run(fault, func(t *testing.T) {
			f, id, p, o := visualObservationFixture(t)
			switch fault {
			case "hash":
				o.Screenshots[0].SHA256 = strings.Repeat("0", 64)
			case "dimensions":
				o.Screenshots[0].Width = 321
			case "route":
				o.Screenshots[0].Route = "other.html"
			case "png":
				o.Screenshots[0].Data = []byte("invalid")
				o.Screenshots[0].SHA256 = hash(o.Screenshots[0].Data)
			case "nonce":
				p.Nonce = "wrong"
			case "machine":
				f.fence.MachineID = 2
			case "expired":
				q := query.BindVerificationTasks(f.db, f.m.names)
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.LeaseExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, e)
			}
			if fault == "expired" {
				_, _, _, _ = bindRealArtifactFiles(t, f)
			}
			prepared, e := f.m.prepareVisualEvidence(f.runner, 7, id, f.fence, p.Nonce, o)
			if fault == "attach_failure" {
				require.NoError(t, e)
				f.m.artifacts.attach = nil
				e = f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.completeVerificationOn(f.runner, tx, 7, id, f.fence, p.Nonce, prepared)
				})
			}
			require.Error(t, e)
			q := query.BindEvidence(f.db, f.m.names)
			count, e := q.WithContext(t.Context()).Where(q.Kind.Eq("FILE")).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestVisualNeverPassesFromExitZeroOrReferenceEquality(t *testing.T) {
	p := VerificationPlan{Method: "VISUAL", HeadCommit: strings.Repeat("a", 40), Argv: []string{"index.html"}}
	zero := 0
	for _, reason := range []string{"PROCESS_EXITED", "BROWSER_UNAVAILABLE", "VISUAL_REVIEW_REQUIRED", "BROWSER_CONSOLE_ERROR"} {
		v, _ := verificationVerdict(p, VerificationObservation{ExitCode: &zero, Reason: reason, HeadCommit: p.HeadCommit})
		require.Equal(t, "INCONCLUSIVE", v)
	}
	for _, args := range [][]string{{}, {"../escape.html"}, {"https://user:pass@example.invalid/"}, {"index.html", "0", "100"}, {"index.html", "100"}} {
		require.False(t, validVisualArguments(args), "%v", args)
	}
}
