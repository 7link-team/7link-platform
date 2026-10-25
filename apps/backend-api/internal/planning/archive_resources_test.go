package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestDefaultPlansExcludeArchivedProjectsBeforePagingAndKeepExplicitHistory(t *testing.T) {
	h := setup(t)
	h.m.entityPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, _ string, actor guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: tenant == 1 && actor == 2 && (project == 3 || project == 4)}, nil
	}
	h.m.project.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }
	h.m.project.ArchivedOn = func(_ context.Context, _ *gorm.DB, _, project guid.ID) (bool, error) { return project == 4, nil }
	for _, id := range []guid.ID{1, 2, 3, 4} {
		project := guid.ID(3)
		if id == 1 || id == 3 {
			project = 4
		}
		row := model.Plan{TenantID: 1, ID: id, ProjectID: project, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-" + id.String(), State: "DRAFT", Content: `{"items":[]}`, ContentHash: strings.Repeat("a", 64), Version: 1}
		require.NoError(t, h.db.Table(h.m.names.Plans.String()).Create(&row).Error)
	}
	for _, sort := range []string{"id", "-id"} {
		in := &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 1, Sort: sort}
		first, err := h.m.listHTTP(claims(t, 2), "plan", in)
		require.NoError(t, err)
		require.Len(t, first.Body.Data.Items, 1)
		require.Equal(t, guid.ID(3), first.Body.Data.Items[0].ProjectID)
		require.True(t, first.Body.Data.HasMore)
		in.Cursor = first.Body.Data.NextCursor
		second, err := h.m.listHTTP(claims(t, 2), "plan", in)
		require.NoError(t, err)
		require.Len(t, second.Body.Data.Items, 1)
		require.Equal(t, guid.ID(3), second.Body.Data.Items[0].ProjectID)
		require.NotEqual(t, first.Body.Data.Items[0].ID, second.Body.Data.Items[0].ID)
		require.False(t, second.Body.Data.HasMore)
	}
	history, err := h.m.listHTTP(claims(t, 2), "plan", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, ProjectID: "4", Limit: 10})
	require.NoError(t, err)
	require.Len(t, history.Body.Data.Items, 2)
	for _, row := range history.Body.Data.Items {
		require.Equal(t, guid.ID(4), row.ProjectID)
	}
	h.m.project.ArchivedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
	_, err = h.m.listHTTP(claims(t, 2), "plan", &PlanningListInput{PlanningTenantInput: PlanningTenantInput{TenantID: "1"}, Limit: 10})
	require.Error(t, err)
}

func TestProjectArchiveRecurringContractNeedsExplicitPause(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	read := func() []delivery.ProjectArchiveResource {
		var out []delivery.ProjectArchiveResource
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.projectArchiveResourcesOn(t.Context(), tx, 1, 3)
			return e
		}))
		return out
	}
	require.False(t, read()[0].Ready)
	q := query.BindLoopContracts(h.db, h.m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).UpdateSimple(q.State.Value("PAUSED"), q.Version.Add(1))
	require.NoError(t, err)
	require.True(t, read()[0].Ready)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		out, e := h.m.projectArchiveResourcesOn(t.Context(), tx, 2, 3)
		require.Empty(t, out)
		return e
	}))
	_, err = h.m.projectArchiveResourcesOn(t.Context(), h.db, 1, 3)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestProjectArchiveSubmittedPlanAndReportRequireHumanConfirmation(t *testing.T) {
	for _, kind := range []string{"plan", "report"} {
		t.Run(kind, func(t *testing.T) {
			h := setup(t)
			goal := goalTree(t, h)
			var record PlanningRecord
			if kind == "plan" {
				record = create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(goal)}}})
			} else {
				record = reportDraft(t, h)
				var body PlanningReportInput
				require.NoError(t, json.Unmarshal(record.Content, &body))
				body.Uncompleted = "One work item remains in progress"
				body.Retrospective = "Use independent verification"
				body.NextPlan = []PlanningPlanItem{linkedItem(goal)}
				body.DeclaredConfidence = "YELLOW"
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					record, err = h.m.editReportOn(claims(t, 2), tx, 1, record.ID, record.Version, body)
					return err
				}))
			}
			read := func() []delivery.ProjectArchiveResource {
				var out []delivery.ProjectArchiveResource
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					out, err = h.m.projectArchiveResourcesOn(t.Context(), tx, 1, 3)
					return err
				}))
				return out
			}
			require.Empty(t, read())
			decide := func(actor int64, action string) {
				require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					in := PlanningDecisionInput{Version: record.Version, ExpectedContentHash: record.ContentHash, Action: action}
					if kind == "plan" {
						record, err = h.m.decidePlanOn(claims(t, guid.ID(actor)), tx, 1, record.ID, in)
					} else {
						record, err = h.m.decideReportOn(claims(t, guid.ID(actor)), tx, 1, record.ID, in)
					}
					return err
				}))
			}
			decide(2, "SUBMIT")
			rows := read()
			require.Len(t, rows, 1)
			require.Equal(t, "SUBMITTED", rows[0].State)
			require.False(t, rows[0].Ready)
			require.Equal(t, record.ID, rows[0].ID)
			decide(3, "CONFIRM")
			require.Empty(t, read())
		})
	}
}

func TestArchiveResourceReadFailuresCannotReportProjectReady(t *testing.T) {
	h := loopHarness(t)
	createTestLoop(t, h, true)
	for _, table := range []string{h.m.names.LoopContracts.String(), h.m.names.LoopRuns.String(), h.m.names.Plans.String(), h.m.names.Reports.String()} {
		t.Run(table, func(t *testing.T) {
			require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("archive_read_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(ErrUnavailable)
				}
			}))
			err := h.db.Transaction(func(tx *gorm.DB) error {
				out, e := h.m.projectArchiveResourcesOn(t.Context(), tx, 1, 3)
				require.Nil(t, out)
				return e
			})
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, h.db.Callback().Query().Remove("archive_read_failure"))
		})
	}
	_, e := h.m.projectArchiveResourcesOn(t.Context(), nil, 1, 3)
	require.ErrorIs(t, e, ErrInvalid)
	p := guid.ID(3)
	h.m.project.ArchivedOn = nil
	require.ErrorIs(t, h.m.loopProjectWriteOn(t.Context(), h.db, 1, &p), ErrUnavailable)
	h.m.project.ArchivedOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
	require.ErrorIs(t, h.m.loopProjectWriteOn(t.Context(), h.db, 1, &p), ErrUnavailable)
}
