package delivery

import (
	"7link.com/platform/pkg/extension/authn"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorkActivityAtomicSequenceRollbackAndActorIsolation(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	input := WorkActivityInput{Kind: "DECISION_MADE", ActorKind: "HUMAN", ActorID: 12, RefKind: "decision", RefID: 88, Title: "Changed criteria"}
	abort := errors.New("later mutation failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		seq, e := m.recordWorkActivityOn(ctx, tx, 1, work.ID, input)
		require.NoError(t, e)
		require.EqualValues(t, 1, seq)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		head, e := m.workActivityHeadOn(ctx, tx, 1, work.ID)
		require.NoError(t, e)
		require.Zero(t, head)
		_, e = m.recordWorkActivityOn(ctx, tx, 1, work.ID, input)
		return e
	}))
	zero := int64(0)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11, SeenSeq: &zero})
		return e
	})
	var interrupted *WorkWriteInterrupted
	require.ErrorAs(t, err, &interrupted)
	require.EqualValues(t, 1, interrupted.CurrentSeq)
	require.Len(t, interrupted.NewerEvents, 1)
	require.Equal(t, guid.ID(12), interrupted.NewerEvents[0].Actor.ID)
	one := int64(1)
	var receipt assignmentReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		receipt, e = m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11, SeenSeq: &one})
		return e
	}))
	require.EqualValues(t, 2, receipt.WorkItem.ActivitySeq)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, e := query.BindWorkItems(tx, m.service.names).WithContext(ctx).First()
		require.NoError(t, e)
		require.NoError(t, m.checkWorkSeenOn(ctx, tx, row, &one), "actor's own assignment activity does not interrupt")
		rows, e := m.workActivitySinceOn(ctx, tx, 1, work.ID, 0, 100)
		require.NoError(t, e)
		require.Len(t, rows, 2)
		require.Equal(t, "ASSIGNED", rows[1].Kind)
		return nil
	}))
}

type unavailableActivityIDs struct{ err error }

func (f unavailableActivityIDs) Next() (int64, error) { return 0, f.err }

func TestWorkActivityPersistenceFailureRollsBackSequenceAndReceipt(t *testing.T) {
	for _, fault := range []string{"ids_missing", "ids", "zero", "create", "update", "query"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			injected := errors.New("activity storage unavailable")
			want := injected
			before, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			switch fault {
			case "ids_missing":
				m.ids = nil
				want = ErrUnavailable
			case "ids":
				m.ids = unavailableActivityIDs{injected}
			case "zero":
				m.ids = unavailableActivityIDs{}
				want = ErrUnavailable
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("activity-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkActivities.String() {
						tx.AddError(injected)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("activity-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkItems.String() {
						tx.AddError(injected)
					}
				}))
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("activity-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.WorkItems.String() {
						tx.AddError(injected)
					}
				}))
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 88, Title: "Updated evidence"})
				return e
			}), want)
			require.NoError(t, db.Callback().Query().Remove("activity-failure"))
			require.NoError(t, db.Callback().Update().Remove("activity-failure"))
			require.NoError(t, db.Callback().Create().Remove("activity-failure"))
			after, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, before, after)
			var count int64
			require.NoError(t, db.Table(m.service.names.WorkActivities.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
	_, db, ctx, work := assignmentHarness(t, true)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := (&Module{}).workActivityHeadOn(ctx, tx, 1, work.ID); return e }), ErrUnavailable)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := (&Module{}).workActivitySinceOn(ctx, tx, 1, work.ID, 0, 10); return e }), ErrUnavailable)
}
func TestWorkActivityBoundariesRejectInvalidInputs(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "UNKNOWN", ActorKind: "SYSTEM", RefKind: "work_item", RefID: work.ID})
		require.ErrorIs(t, e, ErrInvalidArgument)
		_, e = m.workActivityHeadOn(ctx, tx, 2, work.ID)
		require.Error(t, e)
		_, e = m.workActivitySinceOn(ctx, tx, 1, work.ID, -1, 100)
		require.ErrorIs(t, e, ErrInvalidArgument)
		_, e = m.workActivityPageOn(ctx, tx, 1, work.ID, 0, 100, -1)
		require.ErrorIs(t, e, ErrInvalidArgument)
		_, e = m.workActivityPageOn(ctx, tx, 1, work.ID, 0, 201, 0)
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
}

func TestWorkSeenInterruptionIsNotCachedAndSameKeyRetriesAfterReading(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, EntityID: 99, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "DECISION_MADE", ActorKind: "HUMAN", ActorID: 12, RefKind: "decision", RefID: 88, Title: "Changed criteria"})
		return e
	}))
	seen := int64(0)
	input := &assignmentCreateInput{WorkItemID: work.ID.String(), IfMatch: fmtETag(work.Version), IdempotencyKey: "same-logical-claim", Body: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11, SeenSeq: &seen}}
	_, err := m.createAssignmentHTTP(ctx, input)
	require.Error(t, err)
	seen = 1
	created, err := m.createAssignmentHTTP(ctx, input)
	require.NoError(t, err)
	require.EqualValues(t, 2, created.Body.Data.WorkItem.ActivitySeq)
	replay, err := m.createAssignmentHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data.Assignment.ID, replay.Body.Data.Assignment.ID)
}

func TestWorkContextCapabilityLocksAndRequiresCursorWithoutWriting(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.recordWorkActivityOn(ctx, tx, 1, work.ID, WorkActivityInput{Kind: "COMMENT_ADDED", ActorKind: "HUMAN", ActorID: 12, RefKind: "artifact_comment", RefID: 88, Title: "Updated requirement"})
		return err
	}))
	require.Error(t, m.checkWorkContextOn(ctx, db, 1, work.ID, new(int64), true), "the provider requires the source transaction")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, m.checkWorkContextOn(ctx, tx, 1, work.ID, nil, true), ErrWorkSeenSeqRequired)
		var interrupted *WorkWriteInterrupted
		require.ErrorAs(t, m.checkWorkContextOn(ctx, tx, 1, work.ID, new(int64), true), &interrupted)
		require.Equal(t, "work_item:"+work.ID.String(), interrupted.Stream)
		require.Equal(t, "COMMENT_ADDED", interrupted.NewerEvents[0].Kind)
		fresh := int64(1)
		require.NoError(t, m.checkWorkContextOn(ctx, tx, 1, work.ID, &fresh, true))
		future := int64(2)
		require.ErrorIs(t, m.checkWorkContextOn(ctx, tx, 1, work.ID, &future, true), ErrInvalidArgument)
		_, err := m.workActivityHeadOn(ctx, tx, 1, work.ID)
		return err
	}))
}
