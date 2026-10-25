package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestVerificationSourceFactsRequiresExactPersistedScope(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	_, err := f.m.verificationSourceOn(t.Context(), f.db, 7, 9, v.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		got, err := f.m.verificationSourceOn(t.Context(), tx, 7, 9, v.ID)
		require.NoError(t, err)
		require.Equal(t, guid.ID(3), got.WorkItemID)
		require.Equal(t, "CREATED", got.State)
		for _, scope := range [][3]guid.ID{{77, 9, v.ID}, {7, 99, v.ID}, {7, 9, 999}} {
			_, err = f.m.verificationSourceOn(t.Context(), tx, scope[0], scope[1], scope[2])
			require.ErrorIs(t, err, ErrNotFound)
		}
		_, err = f.m.verificationSourceOn(t.Context(), tx, 7, 9, 0)
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
}

func TestLoopExecutionFactsUsesAcceptedReceiptNeverCreationOrInitiator(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	start, end := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	read := func(work []guid.ID) ([]LoopExecutionFact, error) {
		var out []LoopExecutionFact
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.loopExecutionFactsOn(f.runner, tx, 7, 9, work, start, end)
			return e
		})
		return out, err
	}
	r, err := read(nil)
	require.NoError(t, err)
	require.Empty(t, r)
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{Kind: "ACCEPTED", DispatchEpoch: 1, Seq: 1})
	require.NoError(t, err)
	r, err = read(nil)
	require.NoError(t, err)
	require.Len(t, r, 1)
	require.NotNil(t, r[0].StartedAt)
	require.Equal(t, guid.ID(6), r[0].AssignmentID)
	r, err = read([]guid.ID{})
	require.NoError(t, err)
	require.Empty(t, r)
	r, err = read([]guid.ID{99})
	require.NoError(t, err)
	require.Empty(t, r)
	q := query.BindReceipts(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ExecutionID.Eq(v.ID)).Delete()
	require.NoError(t, err)
	r, err = read(nil)
	require.NoError(t, err)
	require.Len(t, r, 1)
	require.Nil(t, r[0].StartedAt)
}

func TestProvideLoopExecutionFactsRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideLoopExecutionFacts(s))
	require.ErrorIs(t, f.m.provideLoopExecutionFacts(s), module.ErrServiceDuplicate)
}
