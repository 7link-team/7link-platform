package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssignmentRenewAndReleaseFailuresPreserveOwnershipAndWork(t *testing.T) {
	for _, operation := range []string{"renew", "release"} {
		for _, fault := range []string{"policy", "missing", "query", "invalid_lease", "assignment_write", "work_write", "transition_write", "ids", "audit"} {
			if operation == "renew" && (fault == "work_write" || fault == "transition_write" || fault == "ids") {
				continue
			}
			if operation == "release" && fault == "invalid_lease" {
				continue
			}
			t.Run(operation+"/"+fault, func(t *testing.T) {
				m, db, ctx, work := assignmentHarness(t, true)
				claimed := assignmentClaim(t, m, db, ctx, work, false)
				failure := errors.New("assignment dependency failed")
				id := claimed.Assignment.ID
				var lease *assignmentLeaseBody
				if operation == "renew" {
					lease = &assignmentLeaseBody{}
				}
				switch fault {
				case "policy":
					m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, failure
					}
				case "missing":
					id++
				case "query":
					require.NoError(t, db.Exec("ALTER TABLE "+m.service.names.Assignments.String()+" RENAME TO unavailable_assignments").Error)
				case "invalid_lease":
					invalid := 1
					lease.TTLSeconds = &invalid
				case "assignment_write", "work_write", "transition_write":
					table, event := m.service.names.Assignments.String(), "UPDATE"
					if fault == "work_write" {
						table = m.service.names.WorkItems.String()
					}
					if fault == "transition_write" {
						table, event = m.service.names.WorkTransitions.String(), "INSERT"
					}
					require.NoError(t, db.Exec("CREATE TRIGGER fail_assignment BEFORE "+event+" ON "+table+" BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error)
				case "ids":
					m.ids = draftIDSource(func() (int64, error) { return 0, failure })
				case "audit":
					m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
				}
				assignmentTable := m.service.names.Assignments.String()
				if fault == "query" {
					assignmentTable = "unavailable_assignments"
				}
				var beforeWork, beforeAssignment []map[string]any
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Find(&beforeWork).Error)
				require.NoError(t, db.Table(assignmentTable).Find(&beforeAssignment).Error)
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := m.changeHumanAssignmentOn(ctx, tx, 1, 99, work.ID, id, 11, 11, claimed.Assignment.Version, lease, nil)
					return err
				})
				require.Error(t, err)
				var afterWork, afterAssignment []map[string]any
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Find(&afterWork).Error)
				require.NoError(t, db.Table(assignmentTable).Find(&afterAssignment).Error)
				require.Equal(t, beforeWork, afterWork)
				require.Equal(t, beforeAssignment, afterAssignment)
			})
		}
	}
}
