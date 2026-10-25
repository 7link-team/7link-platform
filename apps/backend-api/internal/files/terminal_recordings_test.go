package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"testing"
	"time"
)

func TestTerminalChunkReaderClosesPartiallyConsumedStreamAndRejectsMissingChunk(t *testing.T) {
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	_, err = driver.Put(t.Context(), "chunks/one", bytes.NewBufferString("recording"), 9)
	require.NoError(t, err)
	r := &terminalChunkReader{ctx: t.Context(), driver: driver, keys: []string{"chunks/one"}}
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, "rec", string(buf))
	require.NoError(t, r.Close())
	require.Nil(t, r.current)
	require.NoError(t, r.Close())
	r = &terminalChunkReader{ctx: t.Context(), driver: driver, keys: []string{"chunks/missing"}}
	n, err = r.Read(buf)
	require.Zero(t, n)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, r.Close())
}

func TestTerminalRecordingDurableMegabyteTailReplacementAndRollback(t *testing.T) {
	h, artifacts, _, _ := delegatedArtifactFixture(t)
	p := &TerminalRecordings{module: artifacts.module}
	require.NoError(t, p.Bind(func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }))
	const chunkSize = 1 << 20
	var chunks []guid.ID
	appendDurably := func(data []byte) {
		t.Helper()
		var tail guid.ID
		if len(chunks) > 0 {
			tail = chunks[len(chunks)-1]
		}
		staged, replace, err := p.StageAppendChunks(h.ctx, 70, tail, data)
		require.NoError(t, err)
		var next []guid.ID
		next = append(next, chunks...)
		if replace {
			next = next[:len(next)-1]
		}
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			for _, chunk := range staged {
				id, e := p.AttachChunkOn(h.ctx, tx, chunk, 70)
				if e != nil {
					return e
				}
				next = append(next, id)
			}
			if replace {
				return p.ReleaseChunksOn(h.ctx, tx, 70, []guid.ID{tail})
			}
			return nil
		}))
		chunks = next
	}
	first := bytes.Repeat([]byte("a"), chunkSize-17)
	second := bytes.Repeat([]byte("b"), 31)
	third := []byte("recovered")
	appendDurably(first)
	originalTail := chunks[0]
	appendDurably(second)
	require.Len(t, chunks, 2)
	require.NotEqual(t, originalTail, chunks[0])
	completed := chunks[0]
	previousTail := chunks[1]
	// A fresh provider has no memory accumulator; it reads the committed tail.
	p = &TerminalRecordings{module: p.module, authorize: p.authorize}
	appendDurably(third)
	require.Len(t, chunks, 2)
	require.Equal(t, completed, chunks[0], "completed 1 MiB object must never be rewritten")
	require.NotEqual(t, previousTail, chunks[1])
	q := query.Bind(h.db, p.module.bindings.prefix)
	for i, size := range []int64{chunkSize, int64(14 + len(third))} {
		object, err := q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(chunks[i])).First()
		require.NoError(t, err)
		require.Equal(t, size, object.Size)
	}
	for _, id := range []guid.ID{originalTail, previousTail} {
		count, err := q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.ObjectID.Eq(id), q.AccountReference.State.Eq("active")).Count()
		require.NoError(t, err)
		require.Zero(t, count, "superseded partial must be released")
	}
	_, err := p.module.accounts.CollectAccountObjects(h.ctx, h.db, time.Now(), 0, 100)
	require.NoError(t, err)
	for _, id := range []guid.ID{originalTail, previousTail} {
		object, err := q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(id)).First()
		require.NoError(t, err)
		require.Equal(t, "deleted", object.State)
		_, err = p.module.driver.Open(h.ctx, object.StorageKey)
		require.ErrorIs(t, err, ErrNotFound, "superseded bytes must not retain a durable storage leak")
	}
	// Rollback after both attach and release restores the old durable tail and
	// prevents a replacement object from retaining an active recording reference.
	tail := chunks[len(chunks)-1]
	staged, replace, err := p.StageAppendChunks(h.ctx, 70, tail, []byte("uncommitted"))
	require.NoError(t, err)
	require.True(t, replace)
	rollback := errors.New("recording metadata update failed")
	var replacement guid.ID
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		replacement, e = p.AttachChunkOn(h.ctx, tx, staged[0], 70)
		if e != nil {
			return e
		}
		if e = p.ReleaseChunksOn(h.ctx, tx, 70, []guid.ID{tail}); e != nil {
			return e
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	count, err := q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.ObjectID.Eq(tail), q.AccountReference.State.Eq("active")).Count()
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	count, err = q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.ObjectID.Eq(replacement), q.AccountReference.State.Eq("active")).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	// A full tail is retained, and the next event starts a new tail.
	extra, replace, err := p.StageAppendChunks(h.ctx, 70, completed, []byte("next"))
	require.NoError(t, err)
	require.False(t, replace)
	require.Len(t, extra, 1)
	_, _, err = p.StageAppendChunks(h.ctx, 71, tail, []byte("unauthorized"))
	require.ErrorIs(t, err, ErrNotFound)
	prepared, err := p.Seal(h.ctx, 90, 80, 700, 70, chunks)
	require.NoError(t, err)
	reader, err := p.module.driver.Open(h.ctx, prepared.object.object.StorageKey)
	require.NoError(t, err)
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	expected := append(append(append([]byte{}, first...), second...), third...)
	require.Equal(t, expected, actual, "sealed bytes must contain every committed append exactly once")
}

func TestTerminalRecordingRetentionConfiguration(t *testing.T) {
	var missing *TerminalRecordings
	require.Error(t, missing.ConfigureRetention(1))
	p := &TerminalRecordings{}
	require.Equal(t, 180*24*time.Hour, p.retentionDuration())
	require.NoError(t, p.ConfigureRetention(30))
	require.Equal(t, 30*24*time.Hour, p.retentionDuration())
	require.Error(t, p.ConfigureRetention(-1))
	require.Error(t, p.ConfigureRetention(106752))
	require.NoError(t, p.ConfigureRetention(0))
	require.Equal(t, 180*24*time.Hour, p.retentionDuration())
}

func TestTerminalRecordingChunksSurviveCollectionSealExactBytesAndExpire(t *testing.T) {
	h, artifacts, _, _ := delegatedArtifactFixture(t)
	p := &TerminalRecordings{module: artifacts.module}
	require.NoError(t, p.ConfigureRetention(30))
	allowed := true
	require.NoError(t, p.Bind(func(_ context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
		if !allowed || id != 700 || owner.Scope != "entity" || owner.ID != 80 {
			return errOperationForbidden
		}
		return nil
	}))
	require.True(t, p.Available())
	input := []byte("{\"version\":2,\"width\":80,\"height\":24}\n")
	output := []byte("[0.5,\"o\",\"hello\\r\\n\"]\n")
	var chunks []guid.ID
	for _, data := range [][]byte{input, output} {
		chunk, err := p.StageChunk(h.ctx, 70, data)
		require.NoError(t, err)
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			id, e := p.AttachChunkOn(h.ctx, tx, chunk, 70)
			chunks = append(chunks, id)
			return e
		}))
	}
	_, err := p.module.accounts.CollectAccountObjects(h.ctx, h.db, time.Now(), 0, 100)
	require.NoError(t, err)
	prepared, err := p.Seal(h.ctx, 90, 80, 700, 70, chunks)
	require.NoError(t, err)
	var pin PinnedResource
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; pin, e = p.AttachSealedOn(h.ctx, tx, prepared, 700); return e }))
	// Raw immutable chunk keys never bypass audit authorization through account reads.
	rawChunk, err := qForTerminalTest(h.db, p.module.bindings.prefix, chunks[0])
	require.NoError(t, err)
	readable, err := p.module.accounts.AccountKeyReadable(h.ctx, h.db, 70, rawChunk, time.Now())
	require.NoError(t, err)
	require.False(t, readable)
	expected := append(append([]byte{}, input...), output...)
	digest := sha256.Sum256(expected)
	require.Equal(t, hex.EncodeToString(digest[:]), pin.SHA256)
	q := query.Bind(h.db, p.module.bindings.prefix)
	v, err := q.Version.WithContext(h.ctx).Where(q.Version.ID.Eq(pin.VersionID)).First()
	require.NoError(t, err)
	obj, err := q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(v.ObjectID)).First()
	require.NoError(t, err)
	reader, err := p.module.driver.Open(h.ctx, obj.StorageKey)
	require.NoError(t, err)
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, expected, actual)
	allowed = false
	require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: "test.evidence", ResourceKind: "test_evidence", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := (pinnedResources{module: p.module}).PinOn(h.ctx, tx, ResourceBindingInput{ID: 9005, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: &pin.VersionID, ExpectedFileVersion: 2, Purpose: "test.evidence", ResourceKind: "test_evidence", ResourceID: 701, Slot: "copy", ActorID: 70})
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, e := (pinnedResources{module: p.module}).OpenOn(h.ctx, tx, pin.BindingID)
		if e == nil {
			_ = read.Reader.Close()
		}
		return e
	}))
	authorizer := &nodeAuthorizer{}
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := authorizer.authorizeLoaded(h.ctx, tx, storedNodePermissions{managementMode: "resource"}, authz.DataConstraint{AllowAll: true}, authz.DataConstraint{AllowAll: true}, "write")
		return e
	}))
	allowed = true
	node, err := q.Node.WithContext(h.ctx).Where(q.Node.ID.Eq(pin.FileID)).First()
	require.NoError(t, err)
	require.Equal(t, "resource", node.ManagementMode)
	b, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).First()
	require.NoError(t, err)
	require.Equal(t, "terminal_recording", b.Purpose)
	require.NotNil(t, b.ExpiresAt)
	require.InDelta(t, float64(time.Now().Add(30*24*time.Hour).UnixMilli()), float64(*b.ExpiresAt), 5000)
	_, err = p.module.bindings.Expire(h.ctx, h.db, time.Now().Add(31*24*time.Hour), 0, 100)
	require.NoError(t, err)
	b, err = q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).First()
	require.NoError(t, err)
	require.Equal(t, "expired", b.State)
}
func TestTerminalRecordingUnavailableAndChunkBounds(t *testing.T) {
	var p *TerminalRecordings
	require.False(t, p.Available())
	_, err := p.StageChunk(t.Context(), 1, []byte("x"))
	require.Error(t, err)
}

func TestTerminalRecordingAppendAndSealRequireActiveOwnedChunkReferences(t *testing.T) {
	h, a, _, _ := delegatedArtifactFixture(t)
	p := &TerminalRecordings{module: a.module}
	require.NoError(t, p.Bind(func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }))
	require.Error(t, p.Bind(func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }))
	require.Error(t, p.ConfigureRetention(-1))
	require.NoError(t, p.ConfigureRetention(0))
	require.Equal(t, 180*24*time.Hour, p.retentionDuration())
	for _, data := range [][]byte{nil, bytes.Repeat([]byte("x"), 1<<20+1)} {
		_, _, err := p.StageAppendChunks(h.ctx, 70, 0, data)
		require.ErrorIs(t, err, ErrUnsupported)
	}
	_, _, err := p.StageAppendChunks(h.ctx, 70, 999, []byte("x"))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = p.Seal(h.ctx, 90, 80, 700, 70, []guid.ID{999})
	require.ErrorIs(t, err, ErrNotFound)
	chunk, err := p.StageChunk(h.ctx, 70, []byte("recorded"))
	require.NoError(t, err)
	var id guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var err error; id, err = p.AttachChunkOn(h.ctx, tx, chunk, 70); return err }))
	_, _, err = p.StageAppendChunks(h.ctx, 71, id, []byte("x"))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = p.Seal(h.ctx, 90, 80, 700, 71, []guid.ID{id})
	require.ErrorIs(t, err, ErrNotFound)
	fault := errors.New("reference storage unavailable")
	require.NoError(t, p.module.runtime.Database().Write().Callback().Query().Before("gorm:query").Register("test:recording", func(tx *gorm.DB) { tx.AddError(fault) }))
	_, _, err = p.StageAppendChunks(h.ctx, 70, id, []byte("x"))
	require.ErrorIs(t, err, fault)
	_, err = p.Seal(h.ctx, 90, 80, 700, 70, []guid.ID{id})
	require.ErrorIs(t, err, fault)
	require.NoError(t, p.module.runtime.Database().Write().Callback().Query().Remove("test:recording"))
	q := query.Bind(h.db, h.tree.prefix)
	_, err = q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(id)).Update(q.Object.Size, int64(1<<20+1))
	require.NoError(t, err)
	_, _, err = p.StageAppendChunks(h.ctx, 70, id, []byte("x"))
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(id)).Update(q.Object.Size, int64(7))
	require.NoError(t, err)
	// A reference does not authorize a missing object or an unverified one.
	_, err = q.Object.WithContext(h.ctx).Where(q.Object.ID.Eq(id)).Update(q.Object.State, "pending")
	require.NoError(t, err)
	_, _, err = p.StageAppendChunks(h.ctx, 70, id, []byte("x"))
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = p.Seal(h.ctx, 90, 80, 700, 70, []guid.ID{id})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func qForTerminalTest(db *gorm.DB, prefix string, id guid.ID) (string, error) {
	q := query.Bind(db, prefix)
	obj, err := q.Object.WithContext(context.Background()).Where(q.Object.ID.Eq(id)).First()
	if err != nil {
		return "", err
	}
	return obj.StorageKey, nil
}
