package delivery

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

type draftIDSource func() (int64, error)

func (f draftIDSource) Next() (int64, error) { return f() }

func TestProjectDraftPreservesContractValuesAndPersistsInitialState(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "explicit"}[explicit], func(t *testing.T) {
			api, body := projectContractAPI(t)
			payload := `{"name":"项目","code":"AB","contacts":[{"intake_contact_id":"123"}],"repositories":[{"name":"api","remote_url":"git@example.org:team/api.git"}]}`
			if explicit {
				payload = `{"name":"项目","code":"AB","budget_cap_micros":9007199254740993,"budget_notify_pct":50,"target_date":"2028-02-29","contacts":[{"intake_contact_id":"123","receives_deliveries":false,"can_accept":false}],"portal_settings":{"show_ai_label":false,"milestones_visible":false},"repositories":[{"name":"api","remote_url":"git@example.org:team/api.git","protected_branches":[]}]}`
			}
			response := api.Post("/projects", strings.NewReader(payload))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			m := &Module{ids: draftIDSource(func() (int64, error) { return 81, nil })}
			actor := events.Actor{Kind: "HUMAN", ID: 11, AuthoritySource: "ROLE:delivery_lead"}
			now := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.FixedZone("local", 8*3600))
			members := []projectMember{{principalID: 11, roleID: 12}}
			draft, err := m.projectDraft(7, *body, members, actor, collab.ProvisioningAudit{AuthoritySource: actor.AuthoritySource}, now)
			require.NoError(t, err)
			require.Equal(t, core.EntityActive, draft.entity.Status)
			require.Equal(t, map[string]any{"module": "delivery"}, draft.entity.Metadata)
			require.Equal(t, "项目", draft.entity.Name)
			require.Equal(t, "INTAKE", draft.project.Stage)
			require.Equal(t, "HYBRID", draft.project.TeamMode)
			require.Equal(t, "MANUAL", draft.project.DispatchPolicy)
			require.False(t, draft.project.DispatchPolicyExplicit)
			require.Equal(t, 3, draft.project.EnsembleMaxBranches)
			require.Equal(t, now.UTC().Truncate(time.Millisecond), draft.project.StageChangedAt)
			require.Equal(t, draft.project.CreatedAt, draft.project.UpdatedAt)
			require.Equal(t, actor.ID, *draft.project.CreatedBy)
			require.JSONEq(t, `{"due_days":14,"reminder_days":[3,7,12],"escalate_day":14,"deemed_acceptance_days":null}`, draft.project.AcceptancePolicy)
			require.JSONEq(t, "[]", draft.project.Holds)
			require.JSONEq(t, "[]", draft.project.ProvisioningIssues)
			require.Equal(t, "GREEN", draft.project.Health)
			require.Equal(t, "NORMAL", draft.project.BudgetState)
			require.Zero(t, draft.project.BudgetSpentMicros)
			require.Nil(t, draft.project.ArchivedAt)
			require.Nil(t, draft.project.StartedAt)
			require.Equal(t, 1, draft.project.NextDeliverySeq)
			require.Equal(t, 1, draft.project.Version)
			require.Equal(t, guid.ID(81), draft.repositories[0].ID)
			require.Equal(t, "main", draft.repositories[0].DefaultBranch)
			require.Equal(t, !explicit, draft.contacts[0].CanAccept)
			require.Equal(t, !explicit, draft.contacts[0].ReceivesDeliveries)
			members[0].roleID = 99
			require.Equal(t, guid.ID(12), draft.members[0].roleID)
			if explicit {
				require.Equal(t, int64(9007199254740993), *draft.project.BudgetCapMicros)
				require.Equal(t, "2028-02-29", *draft.project.TargetDate)
				require.JSONEq(t, `{"show_ai_label":false,"show_work_item_titles":true,"show_evidence_summary":true,"show_release_notes":true,"contributor_display":"TEAM_ONLY","milestones_visible":false}`, draft.project.PortalSettings)
				require.JSONEq(t, "[]", draft.repositories[0].ProtectedBranches)
			} else {
				require.Nil(t, draft.project.BudgetCapMicros)
				require.Nil(t, draft.project.TargetDate)
				require.Equal(t, 80, draft.project.BudgetNotifyPct)
				require.JSONEq(t, `["main"]`, draft.repositories[0].ProtectedBranches)
			}
			service, db, _ := projectHarness(t)
			draft.project.ProjectID = 99
			table := query.BindProjects(db, service.names)
			require.NoError(t, table.WithContext(t.Context()).Create(&draft.project))
			saved, err := table.WithContext(t.Context()).Where(table.TenantID.Eq(guid.ID(7)), table.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.Equal(t, draft.project.BudgetCapMicros, saved.BudgetCapMicros)
			require.JSONEq(t, draft.project.PortalSettings, saved.PortalSettings)
			require.JSONEq(t, draft.project.AcceptancePolicy, saved.AcceptancePolicy)
		})
	}
}

func TestProjectDraftRejectsUnavailableIDsAndUndefaultedInput(t *testing.T) {
	api, body := projectContractAPI(t)
	response := api.Post("/projects", strings.NewReader(`{"name":"Project","code":"AB","repositories":[{"name":"api","remote_url":"git@example.org:team/api.git"}]}`))
	require.Equal(t, http.StatusOK, response.Code)
	actor := events.Actor{Kind: "HUMAN", ID: 1, AuthoritySource: "ROLE:delivery_lead"}
	audit := collab.ProvisioningAudit{AuthoritySource: actor.AuthoritySource}
	failure := errors.New("ID lease unavailable")
	m := &Module{ids: draftIDSource(func() (int64, error) { return 0, failure })}
	result, err := m.projectDraft(7, *body, nil, actor, audit, time.Now())
	require.ErrorIs(t, err, failure)
	require.Equal(t, projectDraft{}, result)
	m.ids = draftIDSource(func() (int64, error) { return 0, nil })
	_, err = m.projectDraft(7, *body, nil, actor, audit, time.Now())
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.projectDraft(7, projectCreateBody{}, nil, actor, audit, time.Now())
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.projectDraft(7, *body, nil, actor, audit, time.Time{})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = (*Module)(nil).projectDraft(7, *body, nil, actor, audit, time.Now())
	require.ErrorIs(t, err, ErrUnavailable)
	body.Contacts = []projectContactInput{{IntakeContactID: 1}}
	_, err = m.projectDraft(7, *body, nil, actor, audit, time.Now())
	require.ErrorIs(t, err, ErrInvalidArgument)
}
