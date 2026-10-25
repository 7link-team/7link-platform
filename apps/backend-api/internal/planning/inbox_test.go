package planning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type capturePlanningInbox map[string]governance.InboxSource

func (c capturePlanningInbox) Register(s governance.InboxSource) error { c[s.Kind] = s; return nil }

func TestRegisteredPlanningSourcesReportMissingStorageUnavailable(t *testing.T) {
	s := module.NewServices()
	capture := capturePlanningInbox{}
	require.NoError(t, module.Provide[governance.InboxSources](s, capture))
	require.NoError(t, module.Provide[clientwork.CollaborationInbox](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, string) ([]clientwork.CollaborationInboxItem, error) {
		return nil, clientwork.ErrUnavailable
	}))
	require.NoError(t, s.Seal())
	m := &Module{}
	require.NoError(t, m.registerInbox(s))
	require.Len(t, capture, 8)
	require.Contains(t, capture, "PREVIEW_SHARE_APPROVAL")
	for kind, source := range capture {
		_, err := source.Read(claims(t, 3), governance.InboxRequest{TenantID: 1, PrincipalID: 3, Kind: kind, Limit: 10})
		require.ErrorIs(t, err, governance.ErrUnavailable)
	}
}

func TestBlockedReportInboxRequiresActualBlockedSourceAndCurrentOwner(t *testing.T) {
	h := setup(t)
	draft := reportDraft(t, h)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "REPORT_BLOCKED", Limit: 1}
	read := func() []governance.InboxItem {
		rows, err := h.m.reportOverdueInbox(claims(t, 2), req)
		require.NoError(t, err)
		return rows
	}
	require.Empty(t, read(), "a draft is not a blocked source fact")
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", draft.ID).Update("state", "BLOCKED").Error)
	items := read()
	require.Len(t, items, 1)
	require.Equal(t, draft.ID, items[0].ObjectID)
	req.After = &items[0].InboxKey
	require.Empty(t, read())
	req.After = nil
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Empty(t, read(), "current revoked permission removes the source")
}

func TestPlanningInboxUsesLiveIndependentAuthorityBeforeLimit(t *testing.T) {
	h := setup(t)
	when := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{"PLAN_CONFIRMATION", "REPORT_CONFIRMATION"} {
		t.Run(kind, func(t *testing.T) {
			for _, id := range []guid.ID{1001, 1002, 1003, 1004, 1005} {
				project, subject, state, tenant := guid.ID(3), guid.ID(2), "SUBMITTED", guid.ID(1)
				switch id {
				case 1001:
					subject = 3
				case 1002:
					project = 4
				case 1004:
					state = "CONFIRMED"
				case 1005:
					tenant = 2
				}
				if kind == "PLAN_CONFIRMATION" {
					r := model.Plan{ID: id, TenantID: tenant, ProjectID: project, PrincipalID: subject, State: state, PeriodKind: "DAY", PeriodStart: fmt.Sprintf("2026-10-%02d", int(id-1000)), CreatedAt: when}
					require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&r).Error)
				} else {
					r := model.Report{ID: id, TenantID: tenant, ProjectID: project, PrincipalID: subject, State: state, PeriodKind: "DAY", PeriodStart: fmt.Sprintf("2026-10-%02d", int(id-1000)), CreatedAt: when}
					require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&r).Error)
				}
			}
			req := governance.InboxRequest{TenantID: 1, PrincipalID: 3, Kind: kind, Limit: 1}
			items, e := h.m.planningInbox(claims(t, 3), req)
			require.NoError(t, e)
			require.Len(t, items, 1)
			require.Equal(t, guid.ID(1003), items[0].ID)
			require.Equal(t, guid.ID(3), items[0].RecipientID)
			req.After = &items[0].InboxKey
			items, e = h.m.planningInbox(claims(t, 3), req)
			require.NoError(t, e)
			require.Empty(t, items)
			req.After = nil
			req.ProjectID = 4
			items, e = h.m.planningInbox(claims(t, 3), req)
			require.NoError(t, e)
			require.Empty(t, items)
			req.ProjectID = 0
			for _, actor := range []guid.ID{6, 7, 8, 9} {
				req.PrincipalID = actor
				items, e = h.m.planningInbox(claims(t, actor), req)
				require.NoError(t, e)
				require.Empty(t, items)
			}
			req.PrincipalID = 3
			_, e = h.m.planningInbox(context.Background(), req)
			require.ErrorIs(t, e, ErrForbidden)
			req.Kind = "REPORT_OVERDUE"
			_, e = h.m.planningInbox(claims(t, 3), req)
			require.ErrorIs(t, e, ErrInvalid)
		})
	}
}

func TestReportInboxExcludesActualAggregateAuthor(t *testing.T) {
	h := setup(t)
	// The recipient must be the aggregate author's actual superior, not merely
	// an arbitrary principal with report_confirm permission.
	h.m.reportSuperior = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 4, nil }
	r := model.Report{TenantID: 1, ID: 2001, ProjectID: 3, PrincipalID: 0, CreatedBy: 3, State: "SUBMITTED", PeriodKind: "WEEK", PeriodStart: "2026-10-19", CreatedAt: time.Now().UTC()}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Create(&r).Error)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 3, Kind: "REPORT_CONFIRMATION", Limit: 10}
	items, e := h.m.planningInbox(claims(t, 3), req)
	require.NoError(t, e)
	require.Empty(t, items)
	req.PrincipalID = 4
	items, e = h.m.planningInbox(claims(t, 4), req)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, "PROJECT", items[0].SourceScope)
	require.Zero(t, items[0].SourcePrincipalID)
}

func TestOverdueInboxUsesCurrentOwnerScopeAuthorityAndCursor(t *testing.T) {
	h := setup(t)
	draft := reportDraft(t, h)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", draft.ID).Updates(map[string]any{"principal_id": 7, "state": "OVERDUE"}).Error)
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "REPORT_OVERDUE", Limit: 10}
	items, err := h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, guid.ID(7), items[0].SourcePrincipalID)
	require.Equal(t, "MEMBER", items[0].SourceScope)
	key := items[0].InboxKey
	req.After = &key
	items, err = h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, err)
	require.Empty(t, items, "cursor excludes its own key")
	req.After = nil
	req.ProjectID = 4
	items, err = h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, err)
	require.Empty(t, items)
	req.ProjectID = 0
	for _, invalid := range []governance.InboxRequest{
		{TenantID: 1, PrincipalID: 2, Kind: "REPORT_OVERDUE", Limit: 0},
		{TenantID: 1, PrincipalID: 2, Kind: "REPORT_OVERDUE", Limit: 102},
		{TenantID: 1, PrincipalID: 2, Kind: "REPORT_OVERDUE", Limit: 1, ProjectID: -1},
		{TenantID: 1, PrincipalID: 2, Kind: "REPORT_CONFIRMATION", Limit: 1},
	} {
		_, err = h.m.reportOverdueInbox(claims(t, 2), invalid)
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = h.m.reportOverdueInbox(claims(t, 3), req)
	require.ErrorIs(t, err, ErrForbidden)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", draft.ID).Updates(map[string]any{"principal_id": 0, "created_by": 2}).Error)
	items, err = h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "PROJECT", items[0].SourceScope)
	oldPolicy := h.m.entityPolicy
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	items, err = h.m.reportOverdueInbox(claims(t, 2), req)
	require.NoError(t, err)
	require.Empty(t, items, "current revoked permission removes source item")
	h.m.entityPolicy = oldPolicy
}

func TestPlanningInboxProviderErrorsNeverReturnPartialActionItems(t *testing.T) {
	for _, kind := range []string{"PLAN_CONFIRMATION", "REPORT_CONFIRMATION", "REPORT_OVERDUE", "REPORT_BLOCKED"} {
		for _, mode := range []string{"query", "identity", "authority"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				h := setup(t)
				g := goalTree(t, h)
				r := reportDraft(t, h)
				p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
				state := "SUBMITTED"
				actor := guid.ID(3)
				if kind == "REPORT_OVERDUE" {
					state = "OVERDUE"
					actor = 2
				}
				if kind == "REPORT_BLOCKED" {
					state = "BLOCKED"
					actor = 2
				}
				require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("state", state).Error)
				require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Update("state", "SUBMITTED").Error)
				table := h.m.names.Reports.String()
				if kind == "PLAN_CONFIRMATION" {
					table = h.m.names.Plans.String()
				}
				switch mode {
				case "query":
					require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("inbox_source_read", func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							tx.AddError(ErrUnavailable)
						}
					}))
				case "identity":
					h.m.members = failingMemberChecker{ErrUnavailable}
				case "authority":
					h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
						return authz.DataConstraint{}, ErrUnavailable
					}
				}
				req := governance.InboxRequest{TenantID: 1, PrincipalID: actor, Kind: kind, Limit: 10}
				var out []governance.InboxItem
				var err error
				if kind == "REPORT_OVERDUE" || kind == "REPORT_BLOCKED" {
					out, err = h.m.reportOverdueInbox(claims(t, actor), req)
				} else {
					out, err = h.m.planningInbox(claims(t, actor), req)
				}
				require.ErrorIs(t, err, ErrUnavailable)
				require.Empty(t, out)
				require.NoError(t, h.db.Callback().Query().Remove("inbox_source_read"))
			})
		}
	}
}
