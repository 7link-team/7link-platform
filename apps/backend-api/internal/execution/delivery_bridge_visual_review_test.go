package execution

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExecutionVisualReviewAdapterPreservesTransactionAndExactBindings(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "bound"}[available], func(t *testing.T) {
			a := &executionDeliveryAdapter{}
			tx := &gorm.DB{}
			link := WorkLink{TenantID: 1, ProjectID: 2, WorkItemID: 3, ExecutionID: 4, ActorID: 5}
			called := false
			if available {
				a.work.AuthorizeVisualReviewOn = func(_ context.Context, db *gorm.DB, l delivery.ExecutionWorkLink, result guid.ID, version int, criterion guid.ID, head string) error {
					require.Same(t, tx, db)
					require.Equal(t, executionLink(link), l)
					require.Equal(t, guid.ID(6), result)
					require.Equal(t, 7, version)
					require.Equal(t, guid.ID(8), criterion)
					require.Equal(t, "head", head)
					called = true
					return delivery.ErrWorkForbidden
				}
			}
			s := module.NewServices()
			require.NoError(t, a.ProvideServices(s))
			require.NoError(t, s.Seal())
			p, err := module.Resolve[WorkItems](s)
			require.NoError(t, err)
			err = p.AuthorizeVisualReviewOn(t.Context(), tx, link, 6, 7, 8, "head")
			if available {
				require.True(t, called)
				require.ErrorIs(t, err, ErrForbidden)
			} else {
				require.ErrorIs(t, err, ErrCapabilityUnavailable)
			}
		})
	}
}
