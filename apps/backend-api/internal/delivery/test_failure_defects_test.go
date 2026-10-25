package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestFailedTestCreatesCanonicalBugAndPreservesImmutableOccurrence(t *testing.T) {
	for _, fault := range []string{"none", "policy", "source", "owner", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _ := workHarness(t)
			in := TestFailureDefect{Title: "Observed failure", Severity: "CRITICAL", Priority: "HIGH", Reproducibility: "ALWAYS", AccountablePrincipalID: 11, PlanID: 41, PlanVersion: 3, RunID: 42, CaseID: 43, CaseVersion: 2, Steps: "Frozen reproduction", Expected: "Expected behavior", Observed: "Observed failure"}
			switch fault {
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrWorkForbidden
				}
			case "source":
				in.RunID = 0
			case "owner":
				in.AccountablePrincipalID = 0
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, ErrUnavailable })
			}
			var id guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var err error
				id, err = (clientWork{m: m}).CreateOn(ctx, tx, 1, 99, in)
				return err
			})
			if fault != "none" {
				require.Error(t, err)
				q := query.BindWorkItems(db, m.service.names)
				n, e := q.WithContext(ctx).Count()
				require.NoError(t, e)
				require.Zero(t, n)
				return
			}
			require.NoError(t, err)
			out, err := m.readWork(ctx, db, 1, 99, id, 11)
			require.NoError(t, err)
			require.Equal(t, "BUG", out.Type)
			require.Equal(t, "DRAFT", out.State)
			require.Contains(t, out.Description, `"run_id":"42"`)
			require.Contains(t, out.Description, `"case_version":2`)
			require.Equal(t, "CRITICAL", *out.Severity)
			require.Equal(t, "HIGH", *out.Priority)
			require.Equal(t, "Expected behavior", *out.Objective)
		})
	}
}
