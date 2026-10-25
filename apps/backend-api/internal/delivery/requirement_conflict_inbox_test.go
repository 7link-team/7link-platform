package delivery

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type requirementConflictInboxRegistry struct{ source governance.InboxSource }

func (r *requirementConflictInboxRegistry) Register(source governance.InboxSource) error {
	r.source = source
	return nil
}

func TestRequirementConflictInboxRegistersRealSourceAndFailsClosedWithoutLeadCapability(t *testing.T) {
	m, _, ctx, _, _ := readinessHarness(t)
	s := module.NewServices()
	r := &requirementConflictInboxRegistry{}
	require.NoError(t, module.Provide[governance.InboxSources](s, r))
	require.NoError(t, s.Seal())
	require.NoError(t, m.registerRequirementConflictInbox(s))
	require.Equal(t, "REQUIREMENT_CONFLICT", r.source.Kind)
	_, err := r.source.Read(ctx, governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: r.source.Kind, Limit: 1})
	require.ErrorIs(t, err, governance.ErrUnavailable)
}

func TestRequirementConflictInboxUsesCurrentAuthorityAndPersistedOccurrence(t *testing.T) {
	for _, scenario := range []string{"visible", "not_lead", "scope_revoked", "read_revoked", "inactive_human", "not_member", "wrong_project", "resolved", "missing_issue", "executing", "wrong_tenant", "lead_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work, _ := readinessHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			req := governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "REQUIREMENT_CONFLICT", Limit: 1}
			failure := errors.New("lead storage unavailable")
			abort := errors.New("rollback inbox fixture")
			err := db.Transaction(func(tx *gorm.DB) error {
				raised, err := m.requirementConflictOn(ctx, tx, 1, 99, work.ID, 11, 11, requirementConflictBody{Version: work.Version, Action: "RAISE", Reason: "Requirements contradict the agreed acceptance criteria"})
				require.NoError(t, err)
				req.Transaction = tx
				leads := governance.ProjectLead(func(_ context.Context, got *gorm.DB, tenant, project, actor guid.ID) (bool, error) {
					require.Same(t, tx, got, "ACK source checks must use the caller transaction")
					require.Equal(t, guid.ID(1), tenant)
					require.Equal(t, guid.ID(99), project)
					require.Equal(t, guid.ID(11), actor)
					if scenario == "lead_failure" {
						return false, failure
					}
					return scenario != "not_lead", nil
				})
				switch scenario {
				case "scope_revoked", "read_revoked":
					m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
						if scenario == "scope_revoked" && permission == "scope_change" || scenario == "read_revoked" && permission == "work_item_read" {
							return authz.DataConstraint{}, ErrWorkForbidden
						}
						return authz.DataConstraint{AllowAll: true}, nil
					}
				case "inactive_human":
					m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
						return identity.Principal{}, false, nil
					}
				case "not_member":
					m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
				case "wrong_project":
					req.ProjectID = 100
				case "resolved":
					_, err = m.requirementConflictOn(ctx, tx, 1, 99, work.ID, 11, 11, requirementConflictBody{Version: raised.Version, Action: "RESOLVE", Reason: "Human clarified the actual acceptance requirements"})
					require.NoError(t, err)
				case "missing_issue":
					require.NoError(t, tx.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("readiness_issues", "[]").Error)
				case "executing":
					require.NoError(t, tx.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Update("current_execution_id", 81).Error)
				case "wrong_tenant":
					req.TenantID = 2
				}
				items, err := m.requirementConflictInbox(ctx, req, leads)
				if scenario == "wrong_tenant" {
					require.ErrorIs(t, err, governance.ErrForbidden)
				} else if scenario == "lead_failure" {
					require.ErrorIs(t, err, failure)
				} else {
					require.NoError(t, err)
					if scenario == "visible" {
						require.Len(t, items, 1)
						transitions := query.BindWorkTransitions(tx, m.service.names)
						occurrence, err := transitions.WithContext(ctx).Where(transitions.WorkItemID.Eq(work.ID), transitions.ReasonCode.Eq("REQUIREMENT_CONFLICT_RAISE")).First()
						require.NoError(t, err)
						require.Equal(t, occurrence.ID, items[0].ID)
						require.Equal(t, occurrence.CreatedAt, items[0].CreatedAt)
						require.Equal(t, strconv.Itoa(raised.Version), items[0].SourceRevision)
						require.Equal(t, "work_item", items[0].ObjectType)
						require.Equal(t, work.ID, items[0].ObjectID)
						req.After = &items[0].InboxKey
						items, err = m.requirementConflictInbox(ctx, req, leads)
						require.NoError(t, err)
					}
					require.Empty(t, items)
				}
				return abort
			})
			require.ErrorIs(t, err, abort)
		})
	}
}

func TestRequirementConflictInboxFiltersBeforePagination(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		original, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
		require.NoError(t, err)
		second := *original
		second.ID, second.Number = original.ID+1, original.Number+1
		require.NoError(t, q.WithContext(ctx).Create(&second))
		for _, id := range []guid.ID{original.ID, second.ID} {
			_, err = m.requirementConflictOn(ctx, tx, 1, 99, id, 11, 11, requirementConflictBody{Version: 1, Action: "RAISE", Reason: "Requirements contradict the agreed acceptance criteria"})
			require.NoError(t, err)
		}
		// Row scope removes the earlier source before the page size is applied.
		require.NoError(t, tx.Table(m.service.names.WorkItems.String()).Where("id=?", original.ID).Update("created_by", 12).Error)
		m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{OwnerIDs: []string{"11"}}, nil
		}
		items, err := m.requirementConflictInbox(ctx, governance.InboxRequest{Transaction: tx, TenantID: 1, PrincipalID: 11, Kind: "REQUIREMENT_CONFLICT", Limit: 1}, governance.ProjectLead(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }))
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, second.ID, items[0].ObjectID)
		return nil
	}))
	_, err := m.requirementConflictInbox(authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12}), governance.InboxRequest{TenantID: 1, PrincipalID: 11, Kind: "REQUIREMENT_CONFLICT", Limit: 1}, nil)
	require.ErrorIs(t, err, governance.ErrForbidden)
}

func TestRequirementConflictInboxStorageFailuresCannotHideAsResolved(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	injected := errors.New("conflict inbox storage unavailable")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.requirementConflictOn(ctx, tx, 1, 99, work.ID, 11, 11, requirementConflictBody{Version: work.Version, Action: "RAISE", Reason: "Requirements conflict with frozen acceptance"})
		require.NoError(t, e)
		reads, failAt := 0, 0
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("conflict-inbox-failure", func(tx *gorm.DB) {
			reads++
			if reads == failAt {
				tx.AddError(injected)
			}
		}))
		defer db.Callback().Query().Remove("conflict-inbox-failure")
		read := func() error {
			_, e := m.requirementConflictInbox(ctx, governance.InboxRequest{Transaction: tx, TenantID: 1, PrincipalID: 11, ProjectID: 99, Kind: "REQUIREMENT_CONFLICT", Limit: 1}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil })
			return e
		}
		require.NoError(t, read())
		total := reads
		require.Positive(t, total)
		for i := 1; i <= total; i++ {
			reads, failAt = 0, i
			require.Error(t, read(), "conflict read %d", i)
		}
		return nil
	}))
}
