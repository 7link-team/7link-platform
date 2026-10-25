package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ACTIVE is an explicit domain-test fixture, not proof that a new project can
// legitimately reach ACTIVE through the still-pending phase commands.
func assignmentHarness(t *testing.T, ready bool) (*Module, *gorm.DB, context.Context, workDetailView) {
	t.Helper()
	m, db, ctx, work, preparation := readinessHarness(t)
	// A transactional planning test double satisfies MEDIUM-risk assignment launches.
	m.planningRuntime.LaunchOn = func(_ context.Context, tx *gorm.DB, in PlanningLaunch) (guid.ID, error) {
		require.EqualValues(t, 1, in.TenantID)
		require.EqualValues(t, 99, in.ProjectID)
		require.Equal(t, work.ID, in.WorkItemID)
		require.Positive(t, int64(in.AssignmentID))
		return 8001, tx.Exec("INSERT INTO provision_probe(step) VALUES ('planning.created')").Error
	}
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, entity guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if tenant != 1 || entity != 99 {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		require.Contains(t, []string{"work_item_read", "work_item_write", "work_item_execute"}, permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "ACTIVE").Error)
	if ready {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.prepareWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, &preparation)
			return err
		}))
	} else {
		require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("estimate_minutes", 30).Error)
	}
	result, err := m.readWork(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, err)
	return m, db, ctx, result
}
func assignmentClaim(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, work workDetailView, ack bool) assignmentReceipt {
	t.Helper()
	var result assignmentReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11, AcknowledgeReadinessIssues: ack})
		return err
	}))
	return result
}

func TestHumanAssignmentOwnsWorkWithoutFabricatingClaimOrExecution(t *testing.T) {
	for _, ready := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready", false: "explicit_draft_override"}[ready], func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, ready)
			claimed := assignmentClaim(t, m, db, ctx, work, !ready)
			require.Equal(t, "CLAIMED", claimed.WorkItem.State)
			require.Equal(t, work.Version+1, claimed.WorkItem.Version)
			require.Equal(t, int64(14400000), claimed.Assignment.LeaseTTLMS)
			require.WithinDuration(t, claimed.Assignment.CreatedAt.Add(4*time.Hour), *claimed.Assignment.LeaseExpiresAt, time.Millisecond)
			require.Equal(t, "HUMAN", claimed.Assignment.AssigneeKind)
			require.Nil(t, claimed.Assignment.AgentID)
			stored, err := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Nil(t, stored.CurrentClaimID)
			require.Nil(t, stored.CurrentExecutionID)
			require.NotNil(t, stored.FirstClaimedAt)
			require.Equal(t, ready, stored.ReadinessOK)
			if !ready {
				require.Equal(t, guid.ID(11), *stored.ReadinessOverrideBy)
			}
			ttl := 900
			var renewed assignmentReceipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				renewed, e = m.changeHumanAssignmentOn(ctx, tx, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, 1, &assignmentLeaseBody{TTLSeconds: &ttl}, nil)
				return e
			}))
			require.Equal(t, 2, renewed.Assignment.Version)
			require.Equal(t, claimed.WorkItem.Version, renewed.WorkItem.Version)
			require.Equal(t, int64(900000), renewed.Assignment.LeaseTTLMS)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeHumanAssignmentOn(ctx, tx, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, 2, nil, nil)
				return e
			}))
			row, err := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "RELEASED", row.Status)
			require.Nil(t, row.ActiveSlot)
			require.Equal(t, "RELEASED_BY_ASSIGNEE", *row.EndReason)
			require.NotNil(t, row.EndedAt)
			current, err := m.readWork(ctx, db, 1, 99, work.ID, 11)
			require.NoError(t, err)
			if ready {
				require.Equal(t, "READY", current.State)
			} else {
				require.Equal(t, "DRAFT", current.State)
				require.False(t, current.Preparation.OK)
			}
			again := assignmentClaim(t, m, db, ctx, current, !ready)
			require.NotEqual(t, claimed.Assignment.ID, again.Assignment.ID)
			stored, err = query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, claimed.Assignment.CreatedAt, *stored.FirstClaimedAt)
		})
	}
}

func TestHumanAssignmentRejectsInvalidClaimWithoutWrites(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Module, *gorm.DB, workDetailView)
		input  assignmentCreateBody
		want   error
	}{
		"draft_acknowledgement":      {input: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11}, want: ErrAssignmentReadiness},
		"other_assignee":             {input: assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 12}, want: ErrAssignmentHolder},
		"reviewer_not_self_executor": {input: assignmentCreateBody{Role: "REVIEWER", AssigneePrincipalID: 11}, want: ErrAssignmentHolder},
		"inactive_project": {mutate: func(m *Module, db *gorm.DB, _ workDetailView) {
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("tenant_id=1").UpdateColumn("stage", "PLANNING").Error)
		}, want: ErrAssignmentState},
		"paused": {mutate: func(m *Module, db *gorm.DB, _ workDetailView) {
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("tenant_id=1").Update("paused_at", time.Now()).Error)
		}, want: ErrAssignmentState},
		"container": {mutate: func(m *Module, db *gorm.DB, _ workDetailView) {
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("tenant_id=1").Update("is_container", true).Error)
		}, want: ErrAssignmentState},
		"execution_reference": {mutate: func(m *Module, db *gorm.DB, _ workDetailView) {
			require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("tenant_id=1").Update("current_execution_id", 9).Error)
		}, want: ErrUnavailable},
		"revoked_human": {mutate: func(m *Module, _ *gorm.DB, _ workDetailView) {
			m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
				return identity.Principal{}, false, nil
			}
		}, want: ErrInvalidMember},
		"denied_policy": {mutate: func(m *Module, _ *gorm.DB, _ workDetailView) {
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, ErrWorkForbidden
			}
		}, want: ErrWorkForbidden},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, false)
			if test.mutate != nil {
				test.mutate(m, db, work)
			}
			input := test.input
			if input.Role == "" {
				input = assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11, AcknowledgeReadinessIssues: true}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, input)
				return err
			})
			require.ErrorIs(t, err, test.want)
			count, e := query.BindAssignments(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			row, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, work.Version, row.Version)
		})
	}
}

func TestHumanAssignmentContentionHolderVersionAndSuspension(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claimed := assignmentClaim(t, m, db, ctx, work, false)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11})
		return e
	}), ErrAssignmentHeld)
	other := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeHumanAssignmentOn(other, tx, 1, 99, work.ID, claimed.Assignment.ID, 12, 11, 1, nil, nil)
		return e
	}), ErrAssignmentHolder)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeHumanAssignmentOn(ctx, tx, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, 2, nil, nil)
		return e
	}), ErrWorkVersion)
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("tenant_id=1").Update("lease_expires_at", nil).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		receipt, e := m.changeHumanAssignmentOn(ctx, tx, 1, 99, work.ID, claimed.Assignment.ID, 11, 11, 1, &assignmentLeaseBody{}, nil)
		if e == nil {
			require.Nil(t, receipt.Assignment.LeaseExpiresAt)
			require.NotNil(t, receipt.Assignment.LeaseRenewedAt)
		}
		return e
	}))
	// Active-slot uniqueness is enforced independently of the work CAS.
	row, e := query.BindAssignments(db, m.service.names).WithContext(ctx).First()
	require.NoError(t, e)
	duplicate := *row
	duplicate.ID++
	require.Error(t, query.BindAssignments(db, m.service.names).WithContext(ctx).Create(&duplicate))
}

func TestHumanAssignmentRollsBackAuditAndEventFailures(t *testing.T) {
	for _, failure := range []string{"audit", "state_event", "assigned_event"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx, work := assignmentHarness(t, true)
			sentinel := errors.New("transactional effect failed")
			if failure == "audit" {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return sentinel }
			} else {
				m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
					if (failure == "state_event" && event.Type == "work_item.state_changed") || (failure == "assigned_event" && event.Type == "work_item.assigned") {
						return 0, sentinel
					}
					return 1, nil
				})
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.claimHumanWorkOn(ctx, tx, 1, 99, work.ID, 11, 11, work.Version, assignmentCreateBody{Role: "EXECUTOR", AssigneePrincipalID: 11})
				return e
			}), sentinel)
			count, e := query.BindAssignments(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			row, e := query.BindWorkItems(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, "READY", row.State)
			require.Equal(t, work.Version, row.Version)
			require.Nil(t, row.FirstClaimedAt)
		})
	}
}

func TestHumanAssignmentLeaseBounds(t *testing.T) {
	for _, value := range []int{0, 899, 86401, -1} {
		_, err := humanLeaseTTL(&value, 14400000)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	for _, value := range []int{900, 14400, 86400} {
		ttl, err := humanLeaseTTL(&value, 14400000)
		require.NoError(t, err)
		require.Equal(t, int64(value)*1000, ttl)
	}
	_, err := humanLeaseTTL(nil, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
