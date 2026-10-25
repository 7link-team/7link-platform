package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"sync"
	"testing"
	"time"
)

func TestCommentActorClassificationUsesCurrentCanonicalIdentity(t *testing.T) {
	for _, fault := range []string{"human", "principal", "agent", "missing_human", "human_error", "agent_error", "foreign_agent", "foreign_tenant"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			failure := errors.New("identity unavailable")
			f.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				if fault == "human_error" {
					return identity.Principal{}, false, failure
				}
				return identity.Principal{}, fault == "human", nil
			}
			f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "agent_error" {
					return fleet.AgentIdentity{}, false, failure
				}
				a := fleet.AgentIdentity{TenantID: 7, PrincipalID: 21}
				if fault == "foreign_agent" {
					a.PrincipalID++
				}
				if fault == "foreign_tenant" {
					a.TenantID++
				}
				return a, fault != "principal", nil
			}
			if fault == "missing_human" {
				f.m.principals.ActiveHumanByID = nil
			}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				kind, e := f.m.commentActorKindOn(f.human, tx, 7, 21)
				switch fault {
				case "missing_human":
					require.ErrorIs(t, e, ErrCapabilityUnavailable)
					require.Empty(t, kind)
				case "human_error", "agent_error":
					require.ErrorIs(t, e, failure)
					require.Empty(t, kind)
				default:
					require.NoError(t, e)
					want := "PRINCIPAL"
					if fault == "human" {
						want = "HUMAN"
					}
					if fault == "agent" {
						want = "AGENT"
					}
					require.Equal(t, want, kind)
				}
				return nil
			}))
		})
	}
}

func replyFixture(t *testing.T) (*executionFixture, context.Context, guid.ID) {
	f, agent, v := manualEvidenceFixture(t)
	thread := model.ArtifactCommentThread{ID: 701, TenantID: 7, ProjectID: 9, ExecutionID: v.ID, WorkItemID: 3, EvidenceID: 702, Visibility: "INTERNAL", State: "OPEN", AnchorSnapshot: "{}", FirstBody: "Human requirement", CreatedBy: 11, CreatedAt: time.Now().UTC(), Version: 1}
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Create(&thread).Error)
	first := model.ArtifactCommentMessage{ID: 703, TenantID: 7, ThreadID: thread.ID, Seq: 1, Body: thread.FirstBody, CreatedBy: 11, CreatedAt: thread.CreatedAt}
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Create(&first).Error)
	return f, agent, thread.ID
}
func TestArtifactCommentReplyMissingStaleFreshSameKeyCurrentAuthorityAndRollback(t *testing.T) {
	f, agent, thread := replyFixture(t)
	in := &artifactCommentReplyInput{ThreadID: thread.String(), IdempotencyKey: "same-reply", Body: ArtifactCommentReply{Body: "Reviewed the latest requirement"}}
	_, err := f.m.replyArtifactCommentHTTP(agent, in)
	require.Error(t, err)
	stale := int64(0)
	in.Body.SeenSeq = &stale
	_, err = f.m.replyArtifactCommentHTTP(agent, in)
	require.Error(t, err)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	fresh := int64(1)
	in.Body.SeenSeq = &fresh
	created, err := f.m.replyArtifactCommentHTTP(agent, in)
	require.NoError(t, err)
	require.EqualValues(t, 2, created.Body.Data.Seq)
	require.Equal(t, "AGENT", created.Body.Data.ActorKind)
	fresh = 2
	replay, err := f.m.replyArtifactCommentHTTP(agent, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data.ID, replay.Body.Data.ID)
	in.IdempotencyKey = "failed-reply"
	in.Body.Body = "Another reply"
	f.pub.fail = true
	_, err = f.m.replyArtifactCommentHTTP(agent, in)
	require.Error(t, err)
	f.pub.fail = false
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Count(&count).Error)
	require.EqualValues(t, 2, count)
	var current model.ArtifactCommentThread
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Take(&current).Error)
	require.Equal(t, 2, current.Version)
	f.authorized = false
	in.IdempotencyKey = "same-reply"
	in.Body.Body = "Reviewed the latest requirement"
	_, err = f.m.replyArtifactCommentHTTP(agent, in)
	require.Error(t, err)
}
func TestArtifactCommentReplyConcurrentHumansHaveUniqueSequenceAndAgentStateInterruption(t *testing.T) {
	f, agent, thread := replyFixture(t)
	start := make(chan struct{})
	errors := make(chan error, 2)
	sequences := make(chan int64, 2)
	var group sync.WaitGroup
	for _, body := range []string{"First concurrent human reply", "Second concurrent human reply"} {
		group.Add(1)
		go func(body string) {
			defer group.Done()
			<-start
			err := f.db.Transaction(func(tx *gorm.DB) error {
				v, e := f.m.ReplyArtifactCommentOn(f.human, tx, 7, thread, ArtifactCommentReply{Body: body})
				if e == nil {
					sequences <- v.Seq
				}
				return e
			})
			errors <- err
		}(body)
	}
	close(start)
	group.Wait()
	close(errors)
	close(sequences)
	for err := range errors {
		require.NoError(t, err)
	}
	got := []int64{}
	for seq := range sequences {
		got = append(got, seq)
	}
	require.ElementsMatch(t, []int64{2, 3}, got)
	stale := int64(1)
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Stale reply", SeenSeq: &stale})
		var interrupted *delivery.WorkWriteInterrupted
		require.ErrorAs(t, e, &interrupted)
		require.Equal(t, "comment_thread:701", interrupted.Stream)
		require.EqualValues(t, 3, interrupted.CurrentSeq)
		require.Len(t, interrupted.NewerEvents, 2)
		for _, event := range interrupted.NewerEvents {
			require.Equal(t, "HUMAN", event.Actor.Kind)
		}
		return e
	}))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.markArtifactCommentConvertedOn(f.human, tx, 7, thread, "DEFECT", 999, 11)
	}))
	stale = 3
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Read state first", SeenSeq: &stale})
		var interrupted *delivery.WorkWriteInterrupted
		require.ErrorAs(t, e, &interrupted)
		require.Equal(t, "STATE_CHANGED", interrupted.NewerEvents[0].Kind)
		require.Equal(t, "HUMAN", interrupted.NewerEvents[0].Actor.Kind)
		return e
	}))
	stale = 4
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Reopen with new facts", SeenSeq: &stale})
		return e
	}))
	var current model.ArtifactCommentThread
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Take(&current).Error)
	require.Equal(t, "OPEN", current.State)
	require.Equal(t, 5, current.Version)
}

func TestArtifactCommentReplySelfFilteringTruncationAndClientProtection(t *testing.T) {
	f, agent, thread := replyFixture(t)
	seen := int64(0)
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Read legacy context", SeenSeq: &seen})
		var interrupted *delivery.WorkWriteInterrupted
		require.ErrorAs(t, e, &interrupted)
		require.Equal(t, "PRINCIPAL", interrupted.NewerEvents[0].Actor.Kind, "legacy actor type was never recorded")
		return e
	}))
	seen = 1
	for i := 0; i < 2; i++ {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "My own append does not interrupt", SeenSeq: &seen})
			return e
		}))
	}
	for i := 0; i < 51; i++ {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.ReplyArtifactCommentOn(f.human, tx, 7, thread, ArtifactCommentReply{Body: "New Human context"})
			return e
		}))
	}
	seen = 3
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Need refresh", SeenSeq: &seen})
		var interrupted *delivery.WorkWriteInterrupted
		require.ErrorAs(t, e, &interrupted)
		require.True(t, interrupted.Truncated)
		require.Len(t, interrupted.NewerEvents, 50)
		require.EqualValues(t, 54, interrupted.CurrentSeq)
		for _, event := range interrupted.NewerEvents {
			require.Equal(t, "HUMAN", event.Actor.Kind)
		}
		return e
	}))
	read, err := f.m.artifactCommentMessagesHTTP(agent, &artifactCommentMessagesInput{ThreadID: thread.String(), After: 3, Limit: 50})
	require.NoError(t, err)
	require.EqualValues(t, 54, read.Body.Data.LastSeq)
	require.True(t, read.Body.Data.HasMore)
	require.Len(t, read.Body.Data.Items, 50)
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Update("visibility", "CLIENT").Error)
	seen = 54
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "Forbidden team reply", SeenSeq: &seen})
		return e
	}), ErrClientThreadReadOnly)
}

func TestArtifactCommentReplyTwoHundredMessageLimit(t *testing.T) {
	f, agent, thread := replyFixture(t)
	messages := make([]model.ArtifactCommentMessage, 0, 199)
	for seq := int64(2); seq <= 200; seq++ {
		messages = append(messages, model.ArtifactCommentMessage{ID: guid.ID(10000 + seq), TenantID: 7, ThreadID: thread, Seq: seq, ActorKind: "HUMAN", Body: "Prior comment", CreatedBy: 11, CreatedAt: time.Now().UTC()})
	}
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Create(&messages).Error)
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Update("version", 200).Error)
	seen := int64(200)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.ReplyArtifactCommentOn(agent, tx, 7, thread, ArtifactCommentReply{Body: "One too many", SeenSeq: &seen})
		return e
	}), ErrCommentLimit)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Count(&count).Error)
	require.EqualValues(t, 200, count)
}

func TestArtifactCommentReplyEveryStorageFailurePreservesSequenceAndActivity(t *testing.T) {
	for _, command := range []string{"apply"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				f, execution, evidence := pinnedCommentFixture(t)
				in := ArtifactCommentCreate{EvidenceID: evidence, StartLine: 2, EndLine: 2, Body: "Review observed source"}
				var thread ArtifactCommentView
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					var e error
					thread, e = f.m.CreateArtifactCommentOn(f.human, tx, 7, execution, in)
					return e
				}))
				snapshot := func() map[string][]map[string]any {
					out := map[string][]map[string]any{}
					for _, table := range []string{f.m.names.Executions.String(), f.m.names.Receipts.String(), f.m.names.Logs.String(), f.m.names.Evidence.String(), f.m.names.Approvals.String(), f.m.names.CommentThreads.String(), f.m.names.CommentMessages.String(), f.m.names.CommentEvents.String(), "slots", "effects"} {
						var rows []map[string]any
						require.NoError(t, f.db.Table(table).Order("rowid").Find(&rows).Error)
						out[table] = rows
					}
					return out
				}
				before := snapshot()
				count := 0
				injected := errors.New("execution transactional storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("execution_transaction_failure", hook))
				case "create":
					require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("execution_transaction_failure", hook))
				case "update":
					require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("execution_transaction_failure", hook))
				}
				err := f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.ReplyArtifactCommentOn(f.human, tx, 7, thread.ID, ArtifactCommentReply{Body: "Reviewed the source"})
					return e
				})
				require.NoError(t, f.db.Callback().Query().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Create().Remove("execution_transaction_failure"))
				require.NoError(t, f.db.Callback().Update().Remove("execution_transaction_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					return count
				}
				require.ErrorIs(t, err, injected)
				require.Equal(t, before, snapshot(), "failed command must roll back execution, journals, slots and effects")
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestArtifactCommentRepliesRejectsMalformedIdentityAndUnavailableWriter(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		id := "invalid"
		expected := 422
		f := executionHarness(t)
		if unavailable {
			id = "1"
			expected = 503
			f.m.runtime = nil
		}
		t.Run("replyArtifactCommentHTTP"+id, func(t *testing.T) {
			_, err := f.m.replyArtifactCommentHTTP(f.human, &artifactCommentReplyInput{ThreadID: id, IdempotencyKey: "valid"})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
		t.Run("artifactCommentMessagesHTTP"+id, func(t *testing.T) {
			_, err := f.m.artifactCommentMessagesHTTP(f.human, &artifactCommentMessagesInput{ThreadID: id, Limit: 10})
			var status huma.StatusError
			require.ErrorAs(t, err, &status)
			require.Equal(t, expected, status.GetStatus())
		})
	}
}

func TestArtifactCommentReplyHTTPStorageFailuresPreserveSequence(t *testing.T) {
	for _, command := range []string{"reply", "messages"} {
		t.Run(command, func(t *testing.T) {
			executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
				f, _, thread := replyFixture(t)
				return f, func(*gorm.DB) error {
					if command == "reply" {
						out, e := f.m.replyArtifactCommentHTTP(f.human, &artifactCommentReplyInput{ThreadID: thread.String(), IdempotencyKey: "reply-storage", Body: ArtifactCommentReply{Body: "Current review"}})
						if e == nil {
							require.EqualValues(t, 2, out.Body.Data.Seq)
						}
						return e
					}
					out, e := f.m.artifactCommentMessagesHTTP(f.human, &artifactCommentMessagesInput{ThreadID: thread.String(), Limit: 50})
					if e == nil {
						require.Len(t, out.Body.Data.Items, 1)
					}
					return e
				}
			}, true)
		})
	}
}

func TestArtifactCommentThreadOwnerRejectsMissingWriterAndRecord(t *testing.T) {
	for _, missingWriter := range []bool{false, true} {
		f := executionHarness(t)
		if missingWriter {
			f.m.runtime = nil
		}
		_, found, e := f.m.artifactCommentThreadOwner(f.human, 999)
		require.Error(t, e)
		require.False(t, found)
	}
}
