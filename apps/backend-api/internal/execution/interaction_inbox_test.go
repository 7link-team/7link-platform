package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestInteractionNotificationStorageFailuresDoNotDeliverPhantomWake(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, v, in := approvalFixture(t)
		a := requestApproval(t, f, v, in)
		return f, func(tx *gorm.DB) error {
			execution, project, notify, e := f.m.interactionNotificationOn(f.human, tx, 7, a.ID, 11)
			if e == nil {
				require.Equal(t, v.ID, execution)
				require.EqualValues(t, 9, project)
				require.True(t, notify)
			}
			return e
		}
	})
}

func TestInteractionInboxClosesOnDecisionAndCurrentRuntimeFences(t *testing.T) {
	for _, fault := range []string{"decision", "revoked", "lease", "epoch", "cancel", "project", "tenant", "work authority"} {
		t.Run(fault, func(t *testing.T) {
			f, v, in := approvalFixture(t)
			a := requestApproval(t, f, v, in)
			req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 10}
			items, err := f.m.interactionInbox(f.human, req)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, a.ID, items[0].ID)
			checkNotification := func() bool {
				var eligible bool
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					execution, project, ok, err := f.m.interactionNotificationOn(t.Context(), tx, 7, a.ID, 11)
					require.NoError(t, err)
					require.Equal(t, v.ID, execution)
					require.Equal(t, guid.ID(9), project)
					eligible = ok
					return err
				}))
				return eligible
			}
			require.True(t, checkNotification())
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				execution, project, due, active, err := f.m.interactionReminderSourceOn(t.Context(), tx, 7, a.ID)
				require.True(t, active)
				require.Equal(t, v.ID, execution)
				require.EqualValues(t, 9, project)
				require.Equal(t, a.ExpiresAt.Add(-5*time.Minute), due)
				return err
			}))
			q := query.BindExecutions(f.db, f.m.names)
			switch fault {
			case "decision":
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "DENY", "Human rejected exact requested action")
					return e
				}))
			case "revoked":
				f.authorized = false
			case "lease":
				_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.LeaseExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, err)
			case "epoch":
				_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.DispatchEpoch, 2)
				require.NoError(t, err)
			case "cancel":
				_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.CancelRequestedAt, time.Now())
				require.NoError(t, err)
			case "project":
				req.ProjectID = 10
			case "tenant":
				req.TenantID = 8
			case "work authority":
				f.m.work.AuthorizeOn = func(_ context.Context, _ *gorm.DB, _ WorkLink, _ string) error { return ErrForbidden }
			}
			items, err = f.m.interactionInbox(f.human, req)
			if fault == "tenant" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, items)
			if fault != "tenant" && fault != "project" {
				require.False(t, checkNotification())
			}
		})
	}
}

func TestQualityBranchUsesExactWorkReasonTimeAndCurrentAuthority(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	q := query.BindExecutions(f.db, f.m.names)
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Updates(map[string]any{"branch_reason": "QUALITY", "created_at": at}).Error)
	read := func(work int64, before time.Time) (bool, error) {
		var found bool
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			found, err = f.m.qualityBranchBeforeOn(f.human, tx, 7, 9, guid.ID(work), before)
			return err
		})
		return found, err
	}
	found, err := read(3, at.Add(time.Millisecond))
	require.NoError(t, err)
	require.True(t, found)
	found, err = read(3, at)
	require.NoError(t, err)
	require.False(t, found, "only execution before first acceptance counts")
	found, err = read(4, at.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, found)
	_, err = q.WithContext(f.human).Where(q.ID.Eq(v.ID)).Update(q.BranchReason, "INFRA")
	require.NoError(t, err)
	found, err = read(3, at.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, found, "infrastructure retry is not quality rework")
	f.authorized = false
	_, err = read(3, at.Add(time.Hour))
	require.ErrorIs(t, err, ErrForbidden)
}

func TestHeavyProviderUsesCurrentFencedCancellation(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideHeavy(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[HeavyCommands](s)
	require.NoError(t, err)
	require.NotNil(t, c.CreateOn)
	require.NotNil(t, c.ParallelOn, "explicit Heavy branches now provide a guarded parallel launch command")
	v := f.create(t)
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	require.Greater(t, f.read(t, v.ID).Version, v.Version)
	f.authorized = false
	require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := c.CancelOn(f.human, tx, 7, v.ID); return err }))
	require.Nil(t, f.read(t, v.ID).CancelRequestedAt)
	f.authorized = true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		current, err := c.CancelOn(f.human, tx, 7, v.ID)
		require.NotNil(t, current.CancelRequestedAt)
		require.Equal(t, "RUNNING", current.State)
		return err
	}))
}

func TestAttentionExecutionSourceFiltersTenantProjectAndState(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id=?", v.ID).Update("state", "INTERRUPTED").Error)
	for _, project := range []guid.ID{0, 9, 10} {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			out, e := f.m.attentionExecutionsOn(f.human, tx, 7, project)
			require.NoError(t, e)
			if project == 10 {
				require.Empty(t, out)
			} else {
				require.Len(t, out, 1)
				require.Equal(t, v.ID, out[0].ID)
				require.Equal(t, v.WorkItemID, out[0].WorkItemID)
			}
			out, e = f.m.attentionExecutionsOn(f.human, tx, 8, project)
			require.NoError(t, e)
			require.Empty(t, out)
			return nil
		}))
	}
	_, e := f.m.attentionExecutionsOn(f.human, f.db, 7, 9)
	require.ErrorIs(t, e, ErrInvalidArgument)
}
func TestInteractionInboxPaginationAndStorageFailureNeverExposePartialItems(t *testing.T) {
	f, v, in := approvalFixture(t)
	a := requestApproval(t, f, v, in)
	req := governance.InboxRequest{TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 1}
	out, e := f.m.interactionInbox(f.human, req)
	require.NoError(t, e)
	require.Len(t, out, 1)
	require.Equal(t, a.ID, out[0].ID)
	req.After = &out[0].InboxKey
	out, e = f.m.interactionInbox(f.human, req)
	require.NoError(t, e)
	require.Empty(t, out)
	req.After = nil
	for _, bad := range []governance.InboxRequest{{TenantID: 7, PrincipalID: 11, Kind: "OTHER", Limit: 1}, {TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 0}, {TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 102}, {TenantID: 7, PrincipalID: 11, Kind: "INTERACTION", Limit: 1, ProjectID: -1}} {
		_, e = f.m.interactionInbox(f.human, bad)
		require.ErrorIs(t, e, governance.ErrInvalidArgument)
	}
	for at := 1; at <= 3; at++ {
		count := 0
		failure := fmt.Errorf("inbox query %d failed", at)
		require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("inbox_query_failure", func(tx *gorm.DB) {
			count++
			if count == at {
				tx.AddError(failure)
			}
		}))
		out, e = f.m.interactionInbox(f.human, req)
		require.NoError(t, f.db.Callback().Query().Remove("inbox_query_failure"))
		require.ErrorIs(t, e, failure)
		require.Empty(t, out)
	}
	f.m.work.AuthorizeOn = nil
	_, e = f.m.interactionInbox(f.human, req)
	require.ErrorIs(t, e, ErrUnavailable)
	f.m.runtime = nil
	_, e = f.m.interactionInbox(f.human, req)
	require.ErrorIs(t, e, ErrUnavailable)
}

func TestRegisterInteractionInboxRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.registerInteractionInbox(module.NewServices()), module.ErrServicesProviding)
}

func TestProvideHeavyRejectsDuplicateCapabilityOwnership(t *testing.T) {
	f := executionHarness(t)
	s := module.NewServices()
	require.NoError(t, f.m.provideHeavy(s))
	require.ErrorIs(t, f.m.provideHeavy(s), module.ErrServiceDuplicate)
}
