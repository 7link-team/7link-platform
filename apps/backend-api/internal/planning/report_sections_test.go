package planning

import (
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestReportSectionsRequireRootCausesAndExplicitGeneratedDraftReview(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	r := reportDraft(t, h)
	body := PlanningReportInput{Uncompleted: "Waiting", Retrospective: "Generated summary", RetrospectiveAIDrafted: true, RetrospectiveSections: &PlanningRetrospective{WentWell: []string{"Quality"}, Problems: []string{"Delay"}, RootCauses: []string{"Dependency"}}, UncompletedItems: []PlanningUncompleted{{Key: "next", Reason: "OTHER", Detail: "Generated reason", DetailAIDrafted: true, CarryOver: "CARRY_TO_NEXT", Item: linkedItem(g)}}, NextPlan: []PlanningPlanItem{linkedItem(g)}, DeclaredConfidence: "GREEN"}
	encoded, hash, err := hashJSON(body)
	require.NoError(t, err)
	q := query.BindReports(h.db, h.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).Updates(map[string]any{"content": encoded, "content_hash": hash})
	require.NoError(t, err)
	require.False(t, reportSectionsReady(body))
	// Omitted producer flags cannot clear either unchanged draft.
	in := body
	in.RetrospectiveAIDrafted = false
	in.UncompletedItems = append([]PlanningUncompleted(nil), body.UncompletedItems...)
	in.UncompletedItems[0].DetailAIDrafted = false
	require.NoError(t, normalizeReportSections(body, &in))
	require.True(t, in.RetrospectiveAIDrafted)
	require.True(t, in.UncompletedItems[0].DetailAIDrafted)
	in.RetrospectiveConfirmed = true
	in.UncompletedItems[0].DetailConfirmed = true
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = h.m.editReportOn(claims(t, 2), tx, 1, r.ID, r.Version, in)
		return e
	}))
	var saved PlanningReportInput
	require.NoError(t, json.Unmarshal(r.Content, &saved))
	require.False(t, saved.RetrospectiveAIDrafted)
	require.False(t, saved.UncompletedItems[0].DetailAIDrafted)
	require.False(t, saved.RetrospectiveConfirmed)
	require.True(t, reportSectionsReady(saved))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		row, _, e := h.m.reportOn(claims(t, 2), tx, 1, r.ID, "report_read", false)
		if e != nil {
			return e
		}
		check, e := h.m.checkReport(claims(t, 2), tx, 1, row)
		require.NoError(t, e)
		require.NotContains(t, check.Issues, PlanningIssue{Code: "B4_MISSING_SECTION", Blocking: true})
		return nil
	}))
	saved.RetrospectiveSections.RootCauses = nil
	require.False(t, reportSectionsReady(saved))
	saved.RetrospectiveSections.RootCauses = []string{strings.Repeat("x", 501)}
	require.ErrorIs(t, normalizeReportSections(body, &saved), ErrInvalid)
	var untouched model.Report
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id = ?", r.ID).Take(&untouched).Error)
	require.Equal(t, r.ContentHash, untouched.ContentHash)
	edited := body
	edited.Retrospective = "I edited this summary"
	edited.UncompletedItems = append([]PlanningUncompleted(nil), body.UncompletedItems...)
	edited.UncompletedItems[0].Detail = "I edited the reason"
	require.NoError(t, normalizeReportSections(body, &edited))
	require.False(t, edited.RetrospectiveAIDrafted)
	require.False(t, edited.UncompletedItems[0].DetailAIDrafted)
}
