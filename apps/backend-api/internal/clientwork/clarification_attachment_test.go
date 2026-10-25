package clientwork

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type attachmentReader struct {
	io.Reader
	closed bool
}

func (r *attachmentReader) Close() error { r.closed = true; return nil }

type attachmentPermissionProjects struct {
	delivery.ClientWorkAccess
	failure error
}

func (p attachmentPermissionProjects) LockProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if permission == "channel_read" {
		return p.failure
	}
	return p.ClientWorkAccess.LockProjectOn(ctx, tx, tenant, project, permission)
}

func TestAnswerAttachmentDownloadChecksCanonicalQuestionCurrentAuthorityAndExactBytes(t *testing.T) {
	for _, stage := range []string{"success", "other_submitter", "other_recipient", "revoked", "wrong_source", "missing_round", "corrupt_questions", "wrong_question", "missing_handle", "changed_content", "native_denied", "missing_reader", "missing_files"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, clients := clarificationHarness(t)
			require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
			opened, e := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
			require.NoError(t, e)
			answered, e := m.answerSubmissionHTTP(testContext(t, 2), func() *SubmissionAnswerInput {
				in := &SubmissionAnswerInput{ID: "50", RoundID: opened.Body.Data.ID.String(), QuestionNo: 1, Key: "seed-answer", IfMatch: `"1"`}
				in.Body.AnswerText = "Evidence"
				return in
			}())
			require.NoError(t, e)
			require.Equal(t, 2, answered.Body.Data.Version)
			var row model.ClarificationRound
			require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Take(&row).Error)
			view, e := clarificationView(&row)
			require.NoError(t, e)
			file := answerCandidate(91).PinnedResource
			file.BindingID = 901
			file.Purpose = answerAttachmentPurpose
			file.ResourceKind = "clarification_round"
			file.ResourceID = row.ID
			file.Size = 4
			view.Questions[0].Attachments = []AnswerAttachment{{ID: 901, FileID: file.FileID, VersionID: file.VersionID, Name: file.Name, MIME: file.MIME, Size: file.Size, SHA256: file.SHA256}}
			raw, e := json.Marshal(view.Questions)
			require.NoError(t, e)
			require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", row.ID).Update("questions", string(raw)).Error)
			reader := &attachmentReader{Reader: strings.NewReader("test")}
			opens := 0
			m.answerFiles = testAnswerFiles{open: func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
				opens++
				require.EqualValues(t, 901, id)
				if e := m.answerAttachmentResourceOn(ctx, tx, row.ID, files.ResourceOwner{Scope: "entity", ID: 3}); e != nil {
					return files.PinnedResourceRead{}, e
				}
				if stage == "native_denied" {
					return files.PinnedResourceRead{}, files.ErrPinnedForbidden
				}
				if stage == "changed_content" {
					file.SHA256 = strings.Repeat("b", 64)
				}
				require.NoError(t, tx.Exec("INSERT INTO baseline_probe VALUES (901)").Error)
				if stage == "missing_reader" {
					return files.PinnedResourceRead{PinnedResource: file}, nil
				}
				return files.PinnedResourceRead{PinnedResource: file, Reader: reader}, nil
			}}
			in := &SubmissionAttachmentInput{ID: "50", RoundID: row.ID.String(), QuestionNo: 1, AttachmentID: "901"}
			actor := guid.ID(2)
			switch stage {
			case "other_submitter":
				actor = 4
			case "revoked":
				clients.revoked = true
				actor = 4
			case "wrong_source":
				require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", row.ID).Update("intake_id", 999).Error)
			case "missing_round":
				in.RoundID = "999"
			case "corrupt_questions":
				require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", row.ID).Update("questions", "broken").Error)
			case "wrong_question":
				in.QuestionNo = 2
			case "missing_handle":
				in.AttachmentID = "902"
			case "missing_files":
				m.answerFiles = nil
			}
			_, api := humatest.New(t)
			huma.Register(api, huma.Operation{OperationID: "test.answer.content", Method: "GET", Path: "/content"}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
				if stage == "other_recipient" || stage == "revoked" {
					client := guid.ID(5)
					if stage == "revoked" {
						client = 4
					}
					return m.clarificationAttachmentHTTP(testContext(t, client), &ClarificationAttachmentInput{ObjectInput: ObjectInput{EntityID: "3", ID: row.ID.String()}, QuestionNo: 1, AttachmentID: "901"})
				}
				return m.submissionAttachmentHTTP(testContext(t, actor), in)
			})
			response := api.Get("/content")
			var count int64
			require.NoError(t, db.Table("baseline_probe").Count(&count).Error)
			if stage == "success" {
				require.Equal(t, 200, response.Code)
				require.Equal(t, "test", response.Body.String())
				require.Equal(t, "4", response.Header().Get("Content-Length"))
				require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
				require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
				require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
				require.True(t, reader.closed)
				require.EqualValues(t, 1, count)
			} else {
				require.NotEqual(t, 200, response.Code)
				require.Zero(t, count, "failed download must roll back native audit too")
				if stage == "changed_content" {
					require.True(t, reader.closed)
				}
				if stage != "changed_content" && stage != "native_denied" && stage != "missing_reader" {
					require.Zero(t, opens, "denial must precede native bytes")
				}
			}
		})
	}
}

func TestAnswerAttachmentResourceUsesOnlyExistingReaderOrOwnSubmitterGrant(t *testing.T) {
	m, db, projects, _ := clarificationHarness(t)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
	opened, e := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, e)
	run := func(actor guid.ID, owner files.ResourceOwner) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return m.answerAttachmentResourceOn(testContext(t, actor), tx, opened.Body.Data.ID, owner)
		})
	}
	owner := files.ResourceOwner{Scope: "entity", ID: 3}
	require.NoError(t, run(2, owner))
	require.NoError(t, run(4, owner))
	require.Error(t, run(5, owner))
	require.Error(t, run(2, files.ResourceOwner{Scope: "tenant", ID: 1}))
	m.projects = attachmentPermissionProjects{ClientWorkAccess: projects, failure: delivery.ErrProjectForbidden}
	require.NoError(t, run(2, owner), "own submitter does not need channel_read in addition to intake_submit")
	require.Error(t, run(4, owner), "another submitter cannot use the fallback")
	failure := errors.New("reader authority storage unavailable")
	m.projects = attachmentPermissionProjects{ClientWorkAccess: projects, failure: failure}
	require.ErrorIs(t, run(2, owner), failure, "storage failure cannot become alternate permission success")
	for _, in := range []*SubmissionAttachmentInput{{ID: "bad"}, {ID: "50", RoundID: "bad"}, {ID: "50", RoundID: "1", QuestionNo: 0, AttachmentID: "1"}, {ID: "50", RoundID: "1", QuestionNo: 1, AttachmentID: "bad"}} {
		out, e := m.submissionAttachmentHTTP(testContext(t, 2), in)
		require.Error(t, e)
		require.Nil(t, out)
	}
	for _, in := range []*ClarificationAttachmentInput{{ObjectInput: ObjectInput{EntityID: "bad"}}, {ObjectInput: ObjectInput{EntityID: "3", ID: "bad"}}} {
		out, e := m.clarificationAttachmentHTTP(testContext(t, 2), in)
		require.Error(t, e)
		require.Nil(t, out)
	}
}

type testAnswerFiles struct {
	inspect func(context.Context, *gorm.DB, guid.ID) (files.PinnedCandidate, error)
	pin     func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error)
	open    func(context.Context, *gorm.DB, guid.ID) (files.PinnedResourceRead, error)
}

func (f testAnswerFiles) InspectOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
	return f.inspect(ctx, tx, id)
}
func (f testAnswerFiles) PinOn(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
	return f.pin(ctx, tx, in)
}
func (f testAnswerFiles) OpenOn(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
	return f.open(ctx, tx, id)
}

func answerCandidate(id guid.ID) files.PinnedCandidate {
	return files.PinnedCandidate{PinnedResource: files.PinnedResource{SpaceID: 7, FileID: id, VersionID: id + 100, Name: "evidence.txt", MIME: "text/plain", Size: 12, SHA256: strings.Repeat("a", 64)}, NodeVersion: 2}
}

func TestAnswerAttachmentsCommitExactPinsReplayOnceAndRollbackAllBoundaries(t *testing.T) {
	for _, stage := range []string{"success", "inspect", "pin_second", "pin_changed", "ids", "source", "round_update", "audit", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			m, db, projects, _ := clarificationHarness(t)
			require.NoError(t, db.Table("g_intake_records").Where("id=50").Update("submitted_by_principal_id", 2).Error)
			opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
			require.NoError(t, err)
			failure := errors.New("attachment transaction boundary failed")
			pins := 0
			m.answerFiles = testAnswerFiles{
				inspect: func(_ context.Context, tx *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
					_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
					require.True(t, ok)
					if stage == "inspect" && id == 92 {
						return files.PinnedCandidate{}, files.ErrPinnedForbidden
					}
					return answerCandidate(id), nil
				},
				pin: func(ctx context.Context, tx *gorm.DB, in files.ResourceBindingInput) (files.PinnedResource, error) {
					pins++
					require.Equal(t, answerAttachmentPurpose, in.Purpose)
					require.Equal(t, "clarification_round", in.ResourceKind)
					require.Equal(t, opened.Body.Data.ID, in.ResourceID)
					require.EqualValues(t, 2, in.ActorID)
					require.EqualValues(t, 2, in.ExpectedFileVersion)
					require.Equal(t, in.FileID+100, *in.VersionID)
					require.Equal(t, "answer.1."+in.FileID.String(), in.Slot)
					if err := m.answerAttachmentResourceOn(ctx, tx, in.ResourceID, files.ResourceOwner{Scope: "entity", ID: 3}); err != nil {
						return files.PinnedResource{}, err
					}
					if err := tx.Exec("INSERT INTO baseline_probe VALUES (?)", in.ID).Error; err != nil {
						return files.PinnedResource{}, err
					}
					if stage == "pin_second" && pins == 2 {
						return files.PinnedResource{}, failure
					}
					out := answerCandidate(in.FileID).PinnedResource
					out.BindingID = in.ID
					if stage == "pin_changed" {
						out.VersionID++
					}
					return out, nil
				},
			}
			before := collaborationSnapshot(t, m, db)
			if stage == "ids" {
				m.ids = zeroIDs{}
			}
			if stage == "source" {
				m.intakeSources.ClarificationOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, guid.ID, intake.ClarificationAction) (int, error) {
					return 0, failure
				}
			}
			if stage == "round_update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("answer_pin_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.ClarificationRound.String() {
						tx.AddError(failure)
					}
				}))
			}
			if stage == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			if stage == "outbox" {
				m.events = failPublisher{}
			}
			in := &SubmissionAnswerInput{ID: "50", RoundID: opened.Body.Data.ID.String(), QuestionNo: 1, Key: "answer-with-pins", IfMatch: `"1"`}
			in.Body.AnswerText = "See exact evidence"
			in.Body.UploadIDs = []guid.ID{91, 92}
			first, err := m.answerSubmissionHTTP(testContext(t, 2), in)
			require.NoError(t, db.Callback().Update().Remove("answer_pin_failure"))
			if stage != "success" {
				require.Error(t, err)
				require.Nil(t, first)
				require.Equal(t, before, collaborationSnapshot(t, m, db))
				var source struct {
					Status  string
					Version int
				}
				require.NoError(t, db.Table("g_intake_records").Where("id=50").Scan(&source).Error)
				require.Equal(t, "CLARIFYING", source.Status)
				require.Equal(t, 2, source.Version)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 2, pins)
			replay, err := m.answerSubmissionHTTP(testContext(t, 2), in)
			require.NoError(t, err)
			require.Equal(t, "true", replay.Replayed)
			require.Equal(t, first.Body.Data, replay.Body.Data)
			require.Equal(t, 2, pins)
			view, err := m.getSubmissionHTTP(testContext(t, 2), &SubmissionObjectInput{ID: "50"})
			require.NoError(t, err)
			require.Len(t, view.Body.Data.Questions[0].Attachments, 2)
			for i, attachment := range view.Body.Data.Questions[0].Attachments {
				require.Equal(t, in.Body.UploadIDs[i], attachment.FileID)
				require.Equal(t, attachment.FileID+100, attachment.VersionID)
				require.Equal(t, strings.Repeat("a", 64), attachment.SHA256)
				require.Positive(t, attachment.ID)
			}
			var row model.ClarificationRound
			require.NoError(t, db.Table(m.names.ClarificationRound.String()).Where("id=?", opened.Body.Data.ID).Take(&row).Error)
			require.Contains(t, row.Questions, `"attachments"`)
			require.Equal(t, 2, row.Version)
			projects.deny = true
			_, err = m.answerSubmissionHTTP(testContext(t, 2), in)
			require.Error(t, err)
			require.Equal(t, 2, pins, "current authority precedes successful replay")
		})
	}
}

func TestAnswerAttachmentsRejectInvalidCountsIDsTypesAndTotalBeforePinning(t *testing.T) {
	for _, stage := range []string{"none", "too_many", "zero", "duplicate", "missing", "wrong_file", "version", "node_version", "negative_size", "large_file", "large_total", "exe", "sniffed_exe", "bad_mime"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ids := []guid.ID{91, 92}
			pins := 0
			m.answerFiles = testAnswerFiles{inspect: func(_ context.Context, _ *gorm.DB, id guid.ID) (files.PinnedCandidate, error) {
				file := answerCandidate(id)
				switch stage {
				case "wrong_file":
					file.FileID++
				case "version":
					file.VersionID = 0
				case "node_version":
					file.NodeVersion = 0
				case "negative_size":
					file.Size = -1
				case "large_file":
					file.Size = answerAttachmentLimit + 1
				case "large_total":
					file.Size = answerAttachmentLimit/2 + 1
				case "exe":
					file.Name = "evidence.EXE"
				case "sniffed_exe":
					file.MIME = "application/vnd.microsoft.portable-executable"
				case "bad_mime":
					file.MIME = "not a MIME type"
				}
				return file, nil
			}, pin: func(context.Context, *gorm.DB, files.ResourceBindingInput) (files.PinnedResource, error) {
				pins++
				return files.PinnedResource{}, nil
			}}
			switch stage {
			case "none":
				ids = nil
			case "too_many":
				ids = []guid.ID{1, 2, 3, 4, 5, 6}
			case "zero":
				ids[0] = 0
			case "duplicate":
				ids[1] = 91
			case "missing":
				m.answerFiles = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, err := m.pinAnswerAttachmentsOn(testContext(t, 2), tx, 70, 1, ids)
				require.Empty(t, out)
				return err
			})
			if stage == "none" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Zero(t, pins)
		})
	}
	for _, ext := range []string{"exe", "dll", "msi", "bat", "cmd", "com", "scr", "ps1", "vbs", "js", "jar", "apk", "app", "dmg", "sh"} {
		require.False(t, permittedAnswerAttachment("path\\evidence."+strings.ToUpper(ext), "text/plain"), ext)
	}
	for _, media := range []string{"application/x-msdownload", "application/x-ms-installer", "text/x-powershell", "application/x-vbscript", "application/x-sh", "text/x-sh", "application/x-shellscript", "text/javascript", "application/x-javascript", "application/ecmascript", "text/ecmascript", "application/java-archive", "application/x-java-archive", "application/vnd.android.package-archive", "application/x-mach-binary", "application/x-mach-o"} {
		require.False(t, permittedAnswerAttachment("evidence.txt", media), media)
	}
	for _, name := range []string{"evidence.txt", "capture.PNG", "archive.zip", "readme"} {
		require.True(t, permittedAnswerAttachment(name, "text/plain; charset=utf-8"), name)
	}
}
