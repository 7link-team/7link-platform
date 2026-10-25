package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"testing/iotest"
)

func TestArtifactCommentLifecycleHTTPExactReplayCASRevocationAndNoDuplicateEvents(t *testing.T) {
	f, _, thread := replyFixture(t)
	in := &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: "\"1\"", IdempotencyKey: "owned-edit", Body: ArtifactCommentMutation{Body: "Updated via HTTP"}}
	out, err := f.m.artifactCommentEditHTTP(f.human, in)
	require.NoError(t, err)
	require.EqualValues(t, 2, out.Body.Data.LastSeq)
	replayed, err := f.m.artifactCommentEditHTTP(f.human, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, out.Body.Data, replayed.Body.Data)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentEvents.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	in.IdempotencyKey = "stale-edit"
	_, err = f.m.artifactCommentEditHTTP(f.human, in)
	require.Error(t, err)
	in.IdempotencyKey = "owned-edit"
	f.authorized = false
	_, err = f.m.artifactCommentEditHTTP(f.human, in)
	require.Error(t, err)
	f.authorized = true
	state := &artifactCommentMutationInput{ThreadID: thread.String(), IfMatch: "\"2\"", IdempotencyKey: "resolve-thread", Body: ArtifactCommentMutation{State: "RESOLVED"}}
	resolved, err := f.m.artifactCommentStateHTTP(f.human, state)
	require.NoError(t, err)
	require.EqualValues(t, 3, resolved.Body.Data.LastSeq)
	_, err = f.m.artifactCommentStateHTTP(f.human, state)
	require.NoError(t, err)
	withdraw := &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: "\"3\"", IdempotencyKey: "withdraw-comment"}
	deleted, err := f.m.artifactCommentWithdrawHTTP(f.human, withdraw)
	require.NoError(t, err)
	require.Nil(t, deleted.Body.Data.Message.Body)
}

func TestArtifactCommentOwnerLifecycleNeedsCurrentReadWithoutCreateGrant(t *testing.T) {
	f, _, thread := replyFixture(t)
	f.m.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "execution_read"}, nil
	}
	_, err := f.m.replyArtifactCommentHTTP(f.human, &artifactCommentReplyInput{ThreadID: thread.String(), IdempotencyKey: "no-create-grant", Body: ArtifactCommentReply{Body: "forbidden reply"}})
	require.Error(t, err)
	edited, err := f.m.artifactCommentEditHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: "\"1\"", IdempotencyKey: "owner-still-edits", Body: ArtifactCommentMutation{Body: "Owned edit with read access"}})
	require.NoError(t, err)
	require.EqualValues(t, 2, edited.Body.Data.LastSeq)
	_, err = f.m.artifactCommentStateHTTP(f.human, &artifactCommentMutationInput{ThreadID: thread.String(), IfMatch: "\"2\"", IdempotencyKey: "owner-still-resolves", Body: ArtifactCommentMutation{State: "RESOLVED"}})
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.artifactCommentSourceOn(f.human, tx, 7, thread); return e }))
}

func TestArtifactCommentLifecycleHttpRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("artifactCommentSourceHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: ExecutionInput{ExecutionID: id}, EvidenceID: "1"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("artifactCommentStateHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentStateHTTP(f.human, &artifactCommentMutationInput{ThreadID: id, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("artifactCommentReanchorHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentReanchorHTTP(f.human, &artifactCommentMutationInput{ThreadID: id, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("artifactCommentEditHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentEditHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: id, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("artifactCommentWithdrawHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentWithdrawHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: id, IfMatch: "1", IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

type artifactSourcePins struct {
	files.PinnedResources
	pin files.PinnedResourceRead
	err error
}

func (p artifactSourcePins) OpenOn(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error) {
	return p.pin, p.err
}
func TestArtifactSourceHTTPRejectsCorruptionAndClosesEveryOpenedStream(t *testing.T) {
	for _, fault := range []string{"none", "evidence_json", "files_missing", "open_error", "reader_missing", "purpose", "resource", "read_error", "close_error", "binary", "invalid_utf8", "oversize", "hash", "file", "version", "binding"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			f.m.commentMembers = commentMemberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
			v := f.create(t)
			data := "package source\n"
			fact := ArtifactFact{BindingID: 1, FileID: 2, VersionID: 3, SHA256: hash([]byte(data))}
			raw, e := json.Marshal(fact)
			require.NoError(t, e)
			evidence := model.Evidence{ID: 901, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, Kind: "FILE", Title: "Real pinned source", Content: string(raw), ContentHash: hash(raw)}
			if fault == "evidence_json" {
				evidence.Content = "{"
			}
			require.NoError(t, f.db.Table(f.m.names.Evidence.String()).Create(&evidence).Error)
			reader := &artifactAnchorReader{Reader: strings.NewReader(data)}
			failure := errors.New("pinned source unavailable")
			pin := files.PinnedResourceRead{PinnedResource: files.PinnedResource{BindingID: 1, FileID: 2, VersionID: 3, SHA256: fact.SHA256, Name: "source.go", Purpose: "execution_evidence", ResourceID: 901}, Reader: reader}
			switch fault {
			case "purpose":
				pin.Purpose = "other"
			case "resource":
				pin.ResourceID = 902
			case "read_error":
				reader.Reader = iotest.ErrReader(failure)
			case "close_error":
				reader.closeErr = failure
			case "binary":
				reader.Reader = strings.NewReader("\x00")
			case "invalid_utf8":
				reader.Reader = strings.NewReader("\xff")
			case "oversize":
				reader.Reader = strings.NewReader(strings.Repeat("a", 4*1024*1024+1))
			case "hash":
				pin.SHA256 = strings.Repeat("f", 64)
			case "file":
				pin.FileID++
			case "version":
				pin.VersionID++
			case "binding":
				pin.BindingID++
			case "reader_missing":
				pin.Reader = nil
			}
			source := artifactSourcePins{pin: pin}
			if fault == "open_error" {
				source.err = failure
			}
			f.m.commentFiles = source
			if fault == "files_missing" {
				f.m.commentFiles = nil
			}
			out, e := f.m.artifactCommentSourceHTTP(f.human, &artifactCommentSourceInput{ExecutionInput: ExecutionInput{ExecutionID: v.ID.String()}, EvidenceID: "901"})
			if fault == "none" {
				require.NoError(t, e)
				require.Equal(t, data, out.Body.Data.Text)
				require.Equal(t, fact.SHA256, out.Body.Data.SHA256)
			} else {
				require.Error(t, e)
				require.Nil(t, out)
			}
			if fault != "evidence_json" && fault != "files_missing" && fault != "open_error" && fault != "reader_missing" {
				require.Equal(t, 1, reader.closed)
			}
		})
	}
}

func TestArtifactCommentLifecycleHTTPStorageFailuresPreserveChronology(t *testing.T) {
	for _, command := range []string{"edit", "withdraw", "state", "reanchor"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, _, thread := replyFixture(t)
				evidence := guid.ID(0)
				if command == "reanchor" {
					var execution guid.ID
					f, execution, evidence = pinnedCommentFixture(t)
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						v, e := f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review pinned source"})
						thread = v.ID
						return e
					}))
				}
				return f, func(*gorm.DB) error {
					var out *artifactCommentMutationOutput
					var e error
					switch command {
					case "edit":
						out, e = f.m.artifactCommentEditHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: "1", IdempotencyKey: "edit-storage", Body: ArtifactCommentMutation{Body: "Edited source review"}})
					case "withdraw":
						out, e = f.m.artifactCommentWithdrawHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: "1", IdempotencyKey: "withdraw-storage"})
					case "state":
						out, e = f.m.artifactCommentStateHTTP(f.human, &artifactCommentMutationInput{ThreadID: thread.String(), IfMatch: "1", IdempotencyKey: "state-storage", Body: ArtifactCommentMutation{State: "RESOLVED"}})
					case "reanchor":
						out, e = f.m.artifactCommentReanchorHTTP(f.human, &artifactCommentMutationInput{ThreadID: thread.String(), IfMatch: "1", IdempotencyKey: "reanchor-storage", Body: ArtifactCommentMutation{EvidenceID: evidence}})
					}
					if e == nil {
						require.Equal(t, thread, out.Body.Data.ThreadID)
						require.EqualValues(t, 2, out.Body.Data.LastSeq)
					}
					return e
				}
			}, true)
		})
	}
}

func TestCommentLifecycleHTTPRevalidatesPrivilegedActorAndRequestBounds(t *testing.T) {
	for _, command := range []string{"edit", "withdraw", "state"} {
		for _, fault := range []string{"none", "permission", "version", "key", "store"} {
			t.Run(command+"/"+fault, func(t *testing.T) {
				f, _, thread := replyFixture(t)
				require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id=?", thread).Update("created_by", 12).Error)
				require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Where("id=?", 703).Update("created_by", 12).Error)
				if fault == "permission" {
					f.m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, ErrForbidden
					}
				}
				version, key := "1", "privileged-mutation"
				if fault == "version" {
					version = "invalid"
				}
				if fault == "key" {
					key = ""
				}
				if fault == "store" {
					f.m.commentIdempotency = nil
				}
				var e error
				switch command {
				case "edit":
					_, e = f.m.artifactCommentEditHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: version, IdempotencyKey: key, Body: ArtifactCommentMutation{Body: "Other author"}})
				case "withdraw":
					_, e = f.m.artifactCommentWithdrawHTTP(f.human, &artifactCommentMessageMutationInput{CommentID: "703", IfMatch: version, IdempotencyKey: key})
				case "state":
					_, e = f.m.artifactCommentStateHTTP(f.human, &artifactCommentMutationInput{ThreadID: thread.String(), IfMatch: version, IdempotencyKey: key, Body: ArtifactCommentMutation{State: "RESOLVED"}})
				}
				if fault == "none" && command != "edit" {
					require.NoError(t, e)
				} else {
					require.Error(t, e)
				}
			})
		}
	}
}

func TestArtifactCommentMessageOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.artifactCommentMessageOwner(f.human, 999)
		require.Error(t, e)
		require.False(t, found)
	}
}

func TestCommentOwnerResolvesPersistedThreadScope(t *testing.T) {
	f, _, thread := replyFixture(t)
	owner, found, e := f.m.artifactCommentMessageOwner(f.human, 703)
	require.NoError(t, e)
	require.True(t, found)
	require.EqualValues(t, 7, owner.TenantID)
	require.EqualValues(t, 9, owner.EntityID)
	owner, found, e = f.m.artifactCommentThreadOwner(f.human, thread)
	require.NoError(t, e)
	require.True(t, found)
	require.EqualValues(t, 7, owner.TenantID)
	require.EqualValues(t, 9, owner.EntityID)
}
