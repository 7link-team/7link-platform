package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelectedAgentWorkFailuresNeverReturnPartialRows(t *testing.T) {
	for _, scenario := range []string{"invalid_tenant", "invalid_limit", "unavailable", "policy_failure", "invalid_scope", "query_failure", "accountable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, input := workHTTPHarness(t)
			var work workDetailView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				work, err = m.createWorkOn(ctx, tx, 1, 99, 11, input)
				return err
			}))
			failure := errors.New("selected work source failed")
			m.listConstraint = func(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				if scenario == "policy_failure" {
					return authz.DataConstraint{}, failure
				}
				if scenario == "invalid_scope" {
					return authz.DataConstraint{OwnerIDs: []string{"invalid"}}, nil
				}
				return authz.DataConstraint{AllowAll: true}, nil
			}
			if scenario == "unavailable" {
				m.listConstraint = nil
			}
			if scenario == "query_failure" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("selected_work_failure", func(tx *gorm.DB) { _ = tx.AddError(failure) }))
			}
			tenant, limit := guid.ID(1), 10
			if scenario == "invalid_tenant" {
				tenant = 2
			}
			if scenario == "invalid_limit" {
				limit = 0
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				page, e := m.selectedAgentWorkOn(ctx, tx, tenant, work.AccountablePrincipalID, "", limit)
				if scenario == "accountable" {
					require.NoError(t, e)
					require.Len(t, page.Items, 1)
					require.Equal(t, work.ID, page.Items[0].ID)
					require.Equal(t, "accountable", page.Items[0].Responsibility)
				} else {
					require.Empty(t, page.Items)
					require.Empty(t, page.NextCursor)
				}
				return e
			})
			if scenario != "accountable" {
				require.Error(t, err)
			}
		})
	}
}

func TestSelectedAgentWorkUsesViewerScopeAndBoundCursor(t *testing.T) {
	m, db, ctx, input := workHTTPHarness(t)
	agent := guid.ID(77)
	var works []workDetailView
	for i := 0; i < 4; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			r, e := m.createWorkOn(ctx, tx, 1, 99, 11, input)
			works = append(works, r)
			return e
		}))
	}
	at := time.Now().UTC()
	future, past := at.Add(time.Hour), at.Add(-time.Hour)
	for i, r := range works {
		expiry := &future
		if i == 3 {
			expiry = &past
		}
		require.NoError(t, query.BindAssignments(db, m.service.names).WithContext(ctx).Create(&model.Assignment{TenantID: 1, ID: guid.ID(700 + i), ProjectID: 99, WorkItemID: r.ID, ActiveSlot: &r.ID, Role: "EXECUTOR", AssigneeKind: "AGENT", AgentID: &agent, AssigneePrincipalID: agent, Status: "ACTIVE", LeaseTTLMS: 900000, LeaseExpiresAt: expiry, Version: 1, CreatedAt: at, UpdatedAt: at}))
	}
	constraint := authz.DataConstraint{OwnerIDs: []string{"11"}}
	m.listConstraint = func(_ context.Context, tenant guid.ID, permission string, viewer guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, "work_item_read", permission)
		require.Equal(t, guid.ID(11), viewer)
		return constraint, nil
	}
	read := func(cursor string, selected guid.ID) (PersonalWorkPage, error) {
		var out PersonalWorkPage
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.selectedAgentWorkOn(ctx, tx, 1, selected, cursor, 1)
			return e
		})
		return out, e
	}
	first, e := read("", agent)
	require.NoError(t, e)
	require.True(t, first.HasMore)
	require.Len(t, first.Items, 1)
	require.Equal(t, works[2].ID, first.Items[0].ID)
	require.Equal(t, "executor", first.Items[0].Responsibility)
	second, e := read(first.NextCursor, agent)
	require.NoError(t, e)
	require.Equal(t, works[1].ID, second.Items[0].ID)
	_, e = read(first.NextCursor, agent+1)
	require.ErrorIs(t, e, ErrInvalidArgument)
	constraint = authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}
	revoked, e := read(first.NextCursor, agent)
	require.NoError(t, e)
	require.Empty(t, revoked.Items)
	require.False(t, revoked.HasMore)
	constraint = authz.DataConstraint{AllowAll: true}
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"stage": "CLOSED", "archived_at": at}).Error)
	archived, e := read("", agent)
	require.NoError(t, e)
	require.Empty(t, archived.Items)
	_, e = m.selectedAgentWorkOn(ctx, db, 1, agent, "", 1)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestAppendFromIntakeRequiresGenuineTargetAndCanonicalProof(t *testing.T) {
	m, db, ctx, requirement, body := scopeHarness(t)
	project, target := guid.ID(99), requirement.ID
	status := "TRIAGED"
	m.sources = intake.IntakeReader{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return intake.IntakeRef{ID: 50, Kind: "CHANGE", Status: status, ProjectID: &project, TargetRequirementID: &target}, true, nil
	}}
	appendSource := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.appendFromIntakeOn(ctx, tx, 1, 99, requirement.ID, 50, requirement.Version, "cookie", body)
			return e
		})
	}
	body.ReauthProof = "invalid"
	require.Error(t, appendSource())
	body.ReauthProof = "verified"
	target++
	require.ErrorIs(t, appendSource(), ErrInvalidArgument)
	target--
	status = "CONVERTED"
	require.ErrorIs(t, appendSource(), ErrInvalidArgument)
	status = "TRIAGED"
	require.NoError(t, appendSource())
	row, e := query.BindRequirements(db, m.service.names).WithContext(ctx).Where(query.BindRequirements(db, m.service.names).ID.Eq(requirement.ID)).Take()
	require.NoError(t, e)
	require.Equal(t, 2, row.CurrentVersionNo)
	require.ErrorIs(t, appendSource(), ErrRequirementVersion)
}
