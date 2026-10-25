package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestArtifactCommentLifecycleEditingLimitsOwnershipWithdrawalAndImmutableOrigin(t *testing.T) {
	f, _, thread := replyFixture(t)
	edit := func(ctx context.Context, expected int64, body string, retract bool) (out ArtifactCommentMessageView, err error) {
		err = f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.MutateArtifactCommentMessageOn(ctx, tx, 7, thread, 703, expected, retract, ArtifactCommentMutation{Body: body})
			return e
		})
		return
	}
	other := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 12})
	_, err := edit(other, 1, "Cannot edit another author", false)
	require.ErrorIs(t, err, ErrForbidden)
	for i := int64(1); i <= 10; i++ {
		out, e := edit(f.human, i, "Updated original", false)
		require.NoError(t, e)
		require.EqualValues(t, i, out.EditCount)
		require.Equal(t, "Updated original", *out.Body)
	}
	_, err = edit(f.human, 11, "Eleventh edit", false)
	require.ErrorIs(t, err, ErrConflict)
	var original model.ArtifactCommentThread
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Take(&original).Error)
	require.Equal(t, "Human requirement", original.FirstBody)
	out, err := edit(f.human, 11, "", true)
	require.NoError(t, err)
	require.Nil(t, out.Body)
	require.NotNil(t, out.RetractedAt)
	_, err = edit(f.human, 12, "Restore withdrawn text", false)
	require.ErrorIs(t, err, ErrConflict)
	var count int64
	require.NoError(t, f.db.Table(f.m.names.CommentEvents.String()).Count(&count).Error)
	require.EqualValues(t, 11, count)
	read, err := f.m.artifactCommentMessagesHTTP(f.human, &artifactCommentMessagesInput{ThreadID: thread.String(), Limit: 50})
	require.NoError(t, err)
	require.Nil(t, read.Body.Data.Items[0].Body)
	require.EqualValues(t, 12, read.Body.Data.LastSeq)
}

func TestArtifactCommentLifecycleExpiredEditRollbackResolveAndAgentCursor(t *testing.T) {
	f, agent, thread := replyFixture(t)
	require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Where("id = ?", 703).Update("created_at", time.Now().Add(-16*time.Minute)).Error)
	err := f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.MutateArtifactCommentMessageOn(f.human, tx, 7, thread, 703, 1, false, ArtifactCommentMutation{Body: "Too late"})
		return e
	})
	require.ErrorIs(t, err, ErrConflict)
	change := func(ctx context.Context, expected int64, state string, seen *int64) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			_, e := f.m.ChangeArtifactCommentStateOn(ctx, tx, 7, thread, expected, ArtifactCommentMutation{State: state, SeenSeq: seen})
			return e
		})
	}
	f.pub.fail = true
	require.Error(t, change(f.human, 1, "RESOLVED", nil))
	f.pub.fail = false
	var row model.ArtifactCommentThread
	require.NoError(t, f.db.Table(f.m.names.CommentThreads.String()).Where("id = ?", thread).Take(&row).Error)
	require.Equal(t, 1, row.Version)
	require.Equal(t, "OPEN", row.State)
	require.NoError(t, change(f.human, 1, "RESOLVED", nil))
	seen := int64(1)
	err = change(agent, 2, "OPEN", &seen)
	var interruption *delivery.WorkWriteInterrupted
	require.ErrorAs(t, err, &interruption)
	require.EqualValues(t, 2, interruption.CurrentSeq)
	require.Len(t, interruption.NewerEvents, 1)
	seen = 2
	require.NoError(t, change(agent, 2, "OPEN", &seen))
	require.Error(t, change(agent, 3, "RESOLVED", nil))
	// New lifecycle events share the same strictly increasing thread cursor.
	var events []model.ArtifactCommentEvent
	require.NoError(t, f.db.Table(f.m.names.CommentEvents.String()).Order("seq ASC").Find(&events).Error)
	require.Equal(t, []int64{2, 3}, []int64{events[0].Seq, events[1].Seq})
	wrongTenant := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: guid.ID(11)})
	require.Error(t, change(wrongTenant, 3, "RESOLVED", nil))
}

func TestArtifactCommentLifecycleEveryStorageFailurePreservesHistory(t *testing.T) {
	for _, command := range []string{"edit", "retract", "resolve", "reopen"} {
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
				expected := int64(1)
				if command == "reopen" {
					require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
						_, e := f.m.ChangeArtifactCommentStateOn(f.human, tx, 7, thread.ID, 1, ArtifactCommentMutation{State: "RESOLVED"})
						return e
					}))
					expected = 2
				}
				var first model.ArtifactCommentMessage
				require.NoError(t, f.db.Table(f.m.names.CommentMessages.String()).Where("thread_id=? AND seq=1", thread.ID).First(&first).Error)

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
					if command == "edit" || command == "retract" {
						_, e := f.m.MutateArtifactCommentMessageOn(f.human, tx, 7, thread.ID, first.ID, expected, command == "retract", ArtifactCommentMutation{Body: "Corrected observation"})
						return e
					}
					state := "RESOLVED"
					if command == "reopen" {
						state = "OPEN"
					}
					_, e := f.m.ChangeArtifactCommentStateOn(f.human, tx, 7, thread.ID, expected, ArtifactCommentMutation{State: state})
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
