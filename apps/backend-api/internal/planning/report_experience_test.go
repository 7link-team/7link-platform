package planning

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/planning/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestReportExperienceRequiresConfirmedOptInAndProtectsHumanPrivacy(t *testing.T) {
	h := setup(t)
	calls := 0
	h.m.reportExperiences = knowledge.ReportExperiences{ProposeOn: func(_ context.Context, _ *gorm.DB, tenant, report guid.ID, scope knowledge.Scope, body string) (knowledge.EntryView, error) {
		calls++
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(44), report)
		require.Equal(t, "PROJECT", scope.Scope)
		require.Equal(t, guid.ID(3), *scope.ProjectID)
		require.Equal(t, "Review evidence", body)
		return knowledge.EntryView{}, nil
	}}
	row := &model.Report{TenantID: 1, ID: 44, PrincipalID: 2, State: "CONFIRMED"}
	item := PlanningImprovement{Action: "Review evidence", OwnerPrincipalID: 2, DueDate: "2026-10-30"}
	run := func() error {
		b, _ := json.Marshal(PlanningReportInput{ImprovementPlan: []PlanningImprovement{item}})
		row.Content = string(b)
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.proposeReportExperiencesOn(claims(t, 3), tx, row) })
	}
	require.NoError(t, run())
	require.Zero(t, calls)
	item.ProposeMemory = true
	require.ErrorIs(t, run(), ErrInvalid)
	require.Zero(t, calls)
	p := guid.ID(3)
	item.MemoryProjectID = &p
	require.NoError(t, run())
	require.Equal(t, 1, calls)
	row.State = "SUBMITTED"
	require.ErrorIs(t, run(), ErrConflict)
}

func TestExperienceFailureRollsBackConfirmation(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	draft := reportDraft(t, h)
	var body PlanningReportInput
	require.NoError(t, json.Unmarshal(draft.Content, &body))
	body.Uncompleted = "Pending work"
	body.Retrospective = "Review facts"
	body.DeclaredConfidence = "YELLOW"
	body.NextPlan = []PlanningPlanItem{linkedItem(goal)}
	body.ImprovementPlan = []PlanningImprovement{{Action: "Review evidence", OwnerPrincipalID: 2, DueDate: "2026-10-30", ProposeMemory: true}}
	var submitted PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		edited, err := h.m.editReportOn(claims(t, 2), tx, 1, draft.ID, draft.Version, body)
		if err != nil {
			return err
		}
		submitted, err = h.m.decideReportOn(claims(t, 2), tx, 1, draft.ID, PlanningDecisionInput{Version: edited.Version, ExpectedContentHash: edited.ContentHash, Action: "SUBMIT"})
		return err
	}))
	h.m.reportExperiences = knowledge.ReportExperiences{ProposeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, knowledge.Scope, string) (knowledge.EntryView, error) {
		return knowledge.EntryView{}, errors.New("knowledge unavailable")
	}}
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.decideReportOn(claims(t, 3), tx, 1, draft.ID, PlanningDecisionInput{Version: submitted.Version, ExpectedContentHash: submitted.ContentHash, Action: "CONFIRM"})
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		v, err := h.m.readOn(claims(t, 3), tx, 1, draft.ID, "report")
		require.Equal(t, "SUBMITTED", v.State)
		require.Nil(t, v.ConfirmedAt)
		return err
	}))
}

func TestConfirmedAgentImprovementUsesOnlyAgentExperienceScope(t *testing.T) {
	h := setup(t)
	row := &model.Report{TenantID: 1, ID: 44, PrincipalID: 7, State: "CONFIRMED"}
	body := PlanningReportInput{ImprovementPlan: []PlanningImprovement{{Action: "Review evidence", OwnerPrincipalID: 7, DueDate: "2026-10-30", ProposeMemory: true}}}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	row.Content = string(encoded)
	calls := 0
	h.m.reportExperiences = knowledge.ReportExperiences{ProposeOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, scope knowledge.Scope, _ string) (knowledge.EntryView, error) {
		calls++
		require.Equal(t, "AGENT", scope.Scope)
		require.Equal(t, guid.ID(7), *scope.AgentPrincipalID)
		require.Nil(t, scope.ProjectID)
		return knowledge.EntryView{}, nil
	}}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.proposeReportExperiencesOn(claims(t, 3), tx, row) }))
	require.Equal(t, 1, calls)
}

func TestReportExperienceSourceFailuresNeverPublishMemory(t *testing.T) {
	for _, fault := range []string{"malformed", "capability", "cross_project", "agent_capability", "agent_read", "forbidden", "invalid", "unavailable", "storage"} {
		t.Run(fault, func(t *testing.T) {
			h := setup(t)
			item := PlanningImprovement{Action: "Reviewed improvement", ProposeMemory: true}
			row := &model.Report{TenantID: 1, ID: 44, ProjectID: 3, State: "CONFIRMED"}
			called := false
			h.m.reportExperiences.ProposeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, knowledge.Scope, string) (knowledge.EntryView, error) {
				called = true
				switch fault {
				case "forbidden":
					return knowledge.EntryView{}, knowledge.ErrForbidden
				case "invalid":
					return knowledge.EntryView{}, knowledge.ErrInvalid
				case "unavailable":
					return knowledge.EntryView{}, knowledge.ErrUnavailable
				}
				return knowledge.EntryView{}, errors.New("memory storage failed")
			}
			switch fault {
			case "capability":
				h.m.reportExperiences.ProposeOn = nil
			case "cross_project":
				p := guid.ID(9)
				item.MemoryProjectID = &p
			case "agent_capability":
				row.PrincipalID = 7
				h.m.agents.ByPrincipalOn = nil
			case "agent_read":
				row.PrincipalID = 7
				h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			}
			raw, e := json.Marshal(PlanningReportInput{ImprovementPlan: []PlanningImprovement{item}})
			require.NoError(t, e)
			row.Content = string(raw)
			if fault == "malformed" {
				row.Content = "{"
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { return h.m.proposeReportExperiencesOn(claims(t, 3), tx, row) })
			require.Error(t, err)
			require.Equal(t, fault == "forbidden" || fault == "invalid" || fault == "unavailable" || fault == "storage", called)
		})
	}
}
