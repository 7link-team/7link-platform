package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

type inboxTestRegistry struct{}

func TestTemplateDependenciesSeeUncommittedWorkAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			m, db, ctx, in := workHarness(t)
			ctx = authn.WithClaims(ctx, &authn.Claims{PrincipalID: 11, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
			abort := errors.New("rollback template")
			err := db.Transaction(func(tx *gorm.DB) error {
				input := TemplateWorkInput{Title: in.Title, Type: in.Type, Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID, Criteria: []TemplateCriterion{{Text: in.Criteria[0].Text, VerificationMethod: in.Criteria[0].VerificationMethod}}}
				first, err := m.templateWorkOn(ctx, tx, 1, 99, input)
				require.NoError(t, err)
				second, err := m.templateWorkOn(ctx, tx, 1, 99, input)
				require.NoError(t, err)
				require.NoError(t, m.templateDependencyOn(ctx, tx, 1, 99, second, first))
				require.NoError(t, m.templateDependencyOn(ctx, tx, 1, 99, second, first), "replay is a domain no-op")
				require.ErrorIs(t, m.templateDependencyOn(ctx, tx, 1, 99, second+1000, first), ErrProjectNotFound)
				q := query.BindDependencies(tx, m.service.names)
				edges, err := q.WithContext(ctx).Find()
				require.NoError(t, err)
				require.Len(t, edges, 1)
				require.Equal(t, first, edges[0].DependsOnID)
				require.Equal(t, second, edges[0].WorkItemID)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			for _, table := range []string{"d_delivery_work_items", "d_delivery_work_item_dependencies"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				if rollback {
					require.Zero(t, count)
				} else {
					require.Positive(t, count)
				}
			}
		})
	}
}

func (inboxTestRegistry) Register(governance.InboxSource) error { return nil }

func TestReviewInboxUsesExactIndependentUncheckedResult(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 12, Kind: "WORK_ITEM_REVIEW", Limit: 1}
	items, e := h.m.reviewInbox(h.actor(12), req)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, result.ID, items[0].ID)
	require.Equal(t, h.work, items[0].ObjectID)
	req.After = &items[0].InboxKey
	items, e = h.m.reviewInbox(h.actor(12), req)
	require.NoError(t, e)
	require.Empty(t, items)
	req.After = nil
	req.ProjectID = 100
	items, e = h.m.reviewInbox(h.actor(12), req)
	require.NoError(t, e)
	require.Empty(t, items)
	req.ProjectID = 0
	req.PrincipalID = 11
	items, e = h.m.reviewInbox(h.actor(11), req)
	require.NoError(t, e)
	require.Empty(t, items, "producer cannot independently check their own result")
	req.PrincipalID = 12
	_, e = h.m.reviewInbox(h.actor(11), req)
	require.ErrorIs(t, e, ErrWorkForbidden)
	_, e = h.check(result.ID, 12, "PASSED")
	require.NoError(t, e)
	items, e = h.m.reviewInbox(h.actor(12), req)
	require.NoError(t, e)
	require.Empty(t, items, "actual CHECK removes the pending manual check")
}

func TestProvidersUseDomainCommandsAndCurrentAuthority(t *testing.T) {
	m, db, ctx, in := workHarness(t)
	// Existing fixture claims must explicitly carry the matching tenant.
	ctx = authn.WithClaims(ctx, &authn.Claims{PrincipalID: 11, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
	s := module.NewServices()
	require.NoError(t, m.providePlanningTemplates(s))
	require.NoError(t, s.Seal())
	tpl, e := module.Resolve[TemplateInstantiation](s)
	require.NoError(t, e)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		id, e = tpl.CreateWorkOn(ctx, tx, 1, 99, TemplateWorkInput{Title: in.Title, Description: in.Description, Type: in.Type, Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID, Criteria: []TemplateCriterion{{Text: in.Criteria[0].Text, VerificationMethod: in.Criteria[0].VerificationMethod}}})
		return e
	}))
	require.Positive(t, id)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		state, e := tpl.WorkStatusOn(ctx, tx, 1, 99, id)
		require.NoError(t, e)
		require.Equal(t, "DRAFT", state)
		return nil
	}))
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC().Add(time.Hour)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snap, e := m.planningFactsOn(ctx, tx, 1, 99, in.AccountablePrincipalID, start, end)
		require.NoError(t, e)
		require.Len(t, snap.Work, 1)
		require.Equal(t, id, snap.Work[0].ID)
		require.Nil(t, snap.Work[0].AcceptedAt)
		require.Zero(t, snap.ResultCount)
		require.Zero(t, snap.HandoffCount)
		require.Contains(t, snap.MissingSources, "CLIENT_ACCEPTANCE")
		return nil
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.planningFactsOn(ctx, tx, 2, 99, 0, start, end); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.planningFactsOn(ctx, tx, 1, 99, 0, end, start); return e }), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindWorkItems(tx, m.service.names)
		_, e := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ID.Eq(id)).Update(q.State, "ACCEPTED")
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snap, e := m.planningFactsOn(ctx, tx, 1, 99, in.AccountablePrincipalID, start, end)
		require.NoError(t, e)
		require.Nil(t, snap.Work[0].AcceptedAt, "state alone never fabricates acceptance timestamp")
		return nil
	}))
}

func TestReadProvidersRespectCanonicalWorkRowVisibility(t *testing.T) {
	m, db, ctx, in := workHarness(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{PrincipalID: 11, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.templateWorkOn(ctx, tx, 1, 99, TemplateWorkInput{Title: in.Title, Description: in.Description, Type: in.Type, Objective: in.Objective, AccountablePrincipalID: in.AccountablePrincipalID, Criteria: []TemplateCriterion{{Text: in.Criteria[0].Text, VerificationMethod: in.Criteria[0].VerificationMethod}}})
		return err
	}))
	pq := query.BindProjects(db, m.service.names)
	_, err := pq.WithContext(ctx).Where(pq.TenantID.Eq(guid.ID(1)), pq.ProjectID.Eq(guid.ID(99))).Update(pq.CreatedBy, guid.ID(11))
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		constraint authz.DataConstraint
		visible    bool
		creator    guid.ID
	}{
		{"all", authz.DataConstraint{AllowAll: true}, true, 11},
		{"current_creator", authz.DataConstraint{OwnerIDs: []string{"11"}}, true, 11},
		{"other_creator", authz.DataConstraint{OwnerIDs: []string{"11"}}, false, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wq := query.BindWorkItems(db, m.service.names)
			_, err := wq.WithContext(ctx).Where(wq.ID.Eq(id)).Update(wq.CreatedBy, tc.creator)
			require.NoError(t, err)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return tc.constraint, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				state, err := m.templateWorkStatusOn(ctx, tx, 1, 99, id)
				if tc.visible {
					require.NoError(t, err)
					require.Equal(t, "DRAFT", state)
				} else {
					require.Error(t, err)
					require.Empty(t, state)
				}
				facts, err := m.planningFactsOn(ctx, tx, 1, 99, in.AccountablePrincipalID, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Now().UTC().Add(time.Hour))
				require.NoError(t, err)
				if tc.visible {
					require.Len(t, facts.Work, 1)
					require.Equal(t, id, facts.Work[0].ID)
				} else {
					require.Empty(t, facts.Work)
					require.Zero(t, facts.ResultCount)
					require.Zero(t, facts.HandoffCount)
				}
				return nil
			}))
		})
	}
}
