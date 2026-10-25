package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAttentionInteractionStorageFailuresDoNotAuthorizeResponse(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f, v, in := approvalFixture(t)
		a := requestApproval(t, f, v, in)
		return f, func(tx *gorm.DB) error { return f.m.attentionInteractionOn(f.human, tx, 7, 9, 3, a.ID) }
	})
}

func TestAttentionInteractionCurrentApprovalAuthorityAndFences(t *testing.T) {
	for _, fault := range []string{"none", "authority", "inactive", "approval", "expiry", "lease", "epoch", "project", "work"} {
		t.Run(fault, func(t *testing.T) {
			f, v, in := approvalFixture(t)
			a := requestApproval(t, f, v, in)
			project, work := v.ProjectID, v.WorkItemID
			q := query.BindApprovals(f.db, f.m.names)
			runs := query.BindExecutions(f.db, f.m.names)
			switch fault {
			case "authority":
				f.authorized = false
			case "inactive":
				f.humanActive = false
			case "approval":
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
					_, e := f.m.DecideApprovalOn(f.human, tx, 7, v.ID, a.ID, "DENY", "Human denied this exact tool")
					return e
				}))
			case "expiry":
				_, e := q.WithContext(t.Context()).Where(q.ID.Eq(a.ID)).Update(q.ExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, e)
			case "lease":
				_, e := runs.WithContext(t.Context()).Where(runs.ID.Eq(v.ID)).Update(runs.LeaseExpiresAt, time.Now().Add(-time.Second))
				require.NoError(t, e)
			case "epoch":
				_, e := runs.WithContext(t.Context()).Where(runs.ID.Eq(v.ID)).Update(runs.DispatchEpoch, 2)
				require.NoError(t, e)
			case "project":
				project++
			case "work":
				work++
			}
			e := f.db.Transaction(func(tx *gorm.DB) error { return f.m.attentionInteractionOn(f.human, tx, 7, project, work, a.ID) })
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			if fault == "approval" || fault == "expiry" || fault == "lease" || fault == "epoch" || fault == "project" || fault == "work" {
				require.ErrorIs(t, e, delivery.ErrAttentionClosed)
			}
		})
	}
}

func TestBindAttentionInteractionRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindAttentionInteraction(module.NewServices()), module.ErrServicesProviding)
}
