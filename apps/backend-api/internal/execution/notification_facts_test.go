package execution

import (
	"errors"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationFactsCapabilityUsesActualInitiatorAndCallerTransaction(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.ProvideServices(s))
	require.Error(t, f.m.ProvideServices(s))
	require.NoError(t, s.Seal())
	read, err := module.Resolve[NotificationFactsOn](s)
	require.NoError(t, err)
	_, err = module.Resolve[PeriodReader](s)
	require.NoError(t, err)
	v := f.create(t)
	want := NotificationFacts{ProjectID: v.ProjectID, WorkItemID: v.WorkItemID, InitiatorID: 11, MachineID: 1, State: "CREATED", DispatchEpoch: 1}
	readFacts := func() NotificationFacts {
		var facts NotificationFacts
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; facts, e = read(t.Context(), tx, 7, v.ID); return e }))
		return facts
	}
	require.Equal(t, want, readFacts())
	sentinel := errors.New("caller rollback")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		q := query.BindExecutions(tx, f.m.names)
		_, e := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(7)), q.ID.Eq(v.ID)).Updates(map[string]any{"state": "INTERRUPTED", "state_reason": "HEARTBEAT_LOST", "dispatch_epoch": 2})
		require.NoError(t, e)
		facts, e := read(t.Context(), tx, 7, v.ID)
		require.NoError(t, e)
		require.Equal(t, "INTERRUPTED", facts.State)
		require.Equal(t, "HEARTBEAT_LOST", facts.StateReason)
		require.Equal(t, int64(2), facts.DispatchEpoch)
		require.Equal(t, guid.ID(11), facts.InitiatorID)
		return sentinel
	}), sentinel)
	require.Equal(t, want, readFacts())
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "FAILED"})
	require.NoError(t, err)
	want.State, want.StateReason = "FAILED", "RUNTIME_ERROR"
	require.Equal(t, want, readFacts())
	// FAILED is terminal; recovery is an explicit command for INTERRUPTED only.
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, e := f.m.RecoverOn(f.human, tx, 7, v.ID, f.read(t, v.ID).Version, true)
		return e
	}), ErrConflict)
}

func TestNotificationFactsRejectsMissingForeignAndInvalidTransactions(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	for _, tc := range []struct{ tenant, id guid.ID }{{8, v.ID}, {7, v.ID + 1}} {
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.notificationFactsOn(t.Context(), tx, tc.tenant, tc.id); return e }), ErrNotFound)
	}
	for _, tx := range []*gorm.DB{nil, f.db, {}} {
		_, err := f.m.notificationFactsOn(t.Context(), tx, 7, v.ID)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, tc := range []struct{ tenant, id guid.ID }{{0, v.ID}, {7, 0}} {
		require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.notificationFactsOn(t.Context(), tx, tc.tenant, tc.id); return e }), ErrInvalidArgument)
	}
	var missing *Module
	_, err := missing.notificationFactsOn(t.Context(), nil, 7, v.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, f.db.Exec("DROP TABLE ex_execution_executions").Error)
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, e := f.m.notificationFactsOn(t.Context(), tx, 7, v.ID); return e }))
}

func TestProvideNotificationFactsRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideNotificationFacts(s))
	require.ErrorIs(t, f.m.provideNotificationFacts(s), module.ErrServiceDuplicate)
}
