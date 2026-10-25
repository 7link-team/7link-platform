package agents

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestRosterCombinesCurrentHumansAndAuthorizedAgentsBeforeSortingAndPaging(t *testing.T) {
	m, db, _ := agentHarness(t)
	m.teamRoster.ListOn = func(context.Context, *gorm.DB, guid.ID, int, int, string) (core.TeamRosterPage, error) {
		return core.TeamRosterPage{Items: []core.TeamRosterMember{{PrincipalID: 2, Name: "Zoe", State: "active", Roles: []string{"member"}}, {PrincipalID: 11, Name: "Service", State: "active"}}}, nil
	}
	m.rosterPrincipals.ReadOn = func(context.Context, *gorm.DB, []guid.ID) (map[guid.ID]identity.RosterPrincipal, error) {
		return map[guid.ID]identity.RosterPrincipal{2: {Kind: "HUMAN"}, 11: {Kind: "SERVICE_ACCOUNT"}}, nil
	}
	m.profiles.DirectoryOn = func(context.Context, *gorm.DB, guid.ID, fleet.FleetRosterFilter, int, int) (fleet.FleetRosterPage, error) {
		return fleet.FleetRosterPage{Items: []fleet.FleetRosterAgent{{ID: 70, PrincipalID: 7, Name: "Alpha", State: "ACTIVE", ProcessStatus: "RUNNING", MachineOnline: false, MaxParallel: 2}}}, nil
	}
	m.selectedWork.ListOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, p guid.ID, _ string, _ int) (delivery.PersonalWorkPage, error) {
		return delivery.PersonalWorkPage{Items: []delivery.PersonalWorkView{{WorkView: delivery.WorkView{ID: p + 100, Title: "Current", State: "IN_PROGRESS"}}, {WorkView: delivery.WorkView{ID: p + 200, State: "ACCEPTED"}}}}, nil
	}
	m.rosterAnalytics = func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error) {
		return map[guid.ID]RosterAnalytics{7: {FirstDecisions: 5, FirstPassAccepts: 4}, 2: {FirstDecisions: 4, FirstPassAccepts: 4}}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.rosterOn(agentContext(t, 2), tx, 1, RosterInput{Limit: 1, Sort: "-fpa_30d"})
		require.NoError(t, e)
		require.Equal(t, 2, page.Total)
		require.True(t, page.HasMore)
		require.Equal(t, guid.ID(7), page.Items[0].PrincipalID)
		require.Equal(t, "OFFLINE", page.Items[0].Activity)
		require.Equal(t, 1, page.Items[0].Load.ActiveItems)
		require.InDelta(t, .8, *page.Items[0].FPA, .001)
		page, e = m.rosterOn(agentContext(t, 2), tx, 1, RosterInput{Limit: 1, Offset: 1, Sort: "-fpa_30d"})
		require.NoError(t, e)
		require.Equal(t, "HUMAN", page.Items[0].Kind)
		require.Nil(t, page.Items[0].FPA)
		originalPolicy := m.tenantPolicy
		m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
		page, e = m.rosterOn(agentContext(t, 3), tx, 1, RosterInput{Limit: 50, Sort: "name"})
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.Equal(t, "HUMAN", page.Items[0].Kind)
		m.tenantPolicy = originalPolicy
		_, e = m.rosterOn(agentContext(t, 8), tx, 1, RosterInput{Limit: 50})
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.rosterOn(agentContext(t, 2), tx, 1, RosterInput{Limit: 201})
		require.ErrorIs(t, e, ErrInvalid)
		m.rosterAnalytics = nil
		_, e = m.rosterOn(agentContext(t, 2), tx, 1, RosterInput{Limit: 50, Sort: "fpa_30d"})
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}

func TestRosterSortMissingRatesLastAndStableIdentityTies(t *testing.T) {
	rate := .5
	for _, order := range []string{"fpa_30d", "-fpa_30d"} {
		rows := []RosterEntry{{PrincipalID: 9}, {PrincipalID: 7, FPA: &rate}, {PrincipalID: 2}}
		require.NoError(t, sortRoster(rows, order))
		require.Equal(t, []guid.ID{7, 2, 9}, []guid.ID{rows[0].PrincipalID, rows[1].PrincipalID, rows[2].PrincipalID})
	}
	require.ErrorIs(t, sortRoster(nil, "unsupported"), ErrInvalid)
}

func TestRosterCurrentSourceFailuresAndProjectFiltersNeverInventPeopleOrWork(t *testing.T) {
	for _, mode := range []string{"missing-directory", "missing-work", "missing-project", "missing-policy", "failed-project-policy", "invalid-project", "live-read", "normal", "missing", "identity", "membership", "human_source", "principal_source", "project_denied", "project_source", "agent_source", "work_source", "work_cursor", "analytics", "project_filter", "role_filter", "state_filter", "search_filter", "activity_filter"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 2)
			m.teamRoster.ListOn = func(context.Context, *gorm.DB, guid.ID, int, int, string) (core.TeamRosterPage, error) {
				return core.TeamRosterPage{Items: []core.TeamRosterMember{{PrincipalID: 2, State: "active", Roles: []string{"member"}}}}, nil
			}
			m.rosterPrincipals.ReadOn = func(context.Context, *gorm.DB, []guid.ID) (map[guid.ID]identity.RosterPrincipal, error) {
				return map[guid.ID]identity.RosterPrincipal{2: {Kind: "HUMAN", Name: "Human"}}, nil
			}
			m.teamRoster.ProjectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (map[guid.ID][]string, error) {
				return map[guid.ID][]string{2: {"member"}, 7: {"executor"}}, nil
			}
			m.profiles.DirectoryOn = func(context.Context, *gorm.DB, guid.ID, fleet.FleetRosterFilter, int, int) (fleet.FleetRosterPage, error) {
				return fleet.FleetRosterPage{Items: []fleet.FleetRosterAgent{{ID: 70, PrincipalID: 7, Name: "Agent", State: "ACTIVE", ProcessStatus: "RUNNING", MachineOnline: true, MaxParallel: 2}}}, nil
			}
			m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
				return delivery.PersonalWorkPage{Items: []delivery.PersonalWorkView{{WorkView: delivery.WorkView{ID: 100, ProjectID: 3, State: "IN_PROGRESS"}}}}, nil
			}
			in := RosterInput{Limit: 50, Sort: "name"}
			switch mode {
			case "missing-directory":
				m.profiles.DirectoryOn = nil
			case "missing-work":
				m.selectedWork.ListOn = nil
			case "missing-project":
				in.ProjectID = "3"
				m.teamRoster.ProjectOn = nil
			case "missing-policy":
				in.ProjectID = "3"
				m.policy = nil
			case "failed-project-policy":
				in.ProjectID = "3"
				m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, ErrUnavailable
				}
			case "invalid-project":
				in.ProjectID = "bad"
			case "live-read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("roster-live", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.LiveState.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("roster-live")
			case "missing":
				m.teamRoster.ListOn = nil
			case "identity":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, ErrUnavailable
				}
			case "membership":
				m.members = activeMembers{}
			case "human_source":
				m.teamRoster.ListOn = func(context.Context, *gorm.DB, guid.ID, int, int, string) (core.TeamRosterPage, error) {
					return core.TeamRosterPage{}, ErrUnavailable
				}
			case "principal_source":
				m.rosterPrincipals.ReadOn = func(context.Context, *gorm.DB, []guid.ID) (map[guid.ID]identity.RosterPrincipal, error) {
					return nil, ErrUnavailable
				}
			case "project_denied":
				in.ProjectID = "3"
				m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
			case "project_source":
				in.ProjectID = "3"
				m.teamRoster.ProjectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (map[guid.ID][]string, error) {
					return nil, ErrUnavailable
				}
			case "agent_source":
				m.profiles.DirectoryOn = func(context.Context, *gorm.DB, guid.ID, fleet.FleetRosterFilter, int, int) (fleet.FleetRosterPage, error) {
					return fleet.FleetRosterPage{}, fleet.ErrUnavailable
				}
			case "work_source":
				m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
					return delivery.PersonalWorkPage{}, ErrUnavailable
				}
			case "work_cursor":
				m.selectedWork.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, int) (delivery.PersonalWorkPage, error) {
					return delivery.PersonalWorkPage{HasMore: true}, nil
				}
			case "analytics":
				m.rosterAnalytics = func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error) {
					return nil, ErrUnavailable
				}
			case "project_filter":
				in.ProjectID = "4"
				m.teamRoster.ProjectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (map[guid.ID][]string, error) {
					return map[guid.ID][]string{}, nil
				}
			case "role_filter":
				in.Kind = "HUMAN"
				in.Role = "other"
			case "state_filter":
				in.Kind = "HUMAN"
				in.State = "INACTIVE"
			case "search_filter":
				in.Kind = "HUMAN"
				in.Search = "unmatched"
			case "activity_filter":
				in.Kind = "AGENT"
				in.Activity = "ERROR"
			}
			if mode == "membership" {
				ctx = agentContext(t, 8)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				page, e := m.rosterOn(ctx, tx, 1, in)
				if mode == "normal" {
					require.NoError(t, e)
					require.Len(t, page.Items, 2)
					require.Equal(t, "ONLINE", page.Items[0].Activity)
				}
				if strings.HasSuffix(mode, "filter") {
					require.NoError(t, e)
					require.Empty(t, page.Items)
				}
				return e
			})
			if mode != "normal" && !strings.HasSuffix(mode, "filter") {
				require.Error(t, err)
			}
		})
	}
}

func TestRosterOrderingUsesPublishedKeysAndDeterministicTies(t *testing.T) {
	maximum := 2
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	low, high := .2, .8
	for _, order := range []string{"name", "kind", "origin", "state", "created_at", "load_ratio", "fpa_30d"} {
		first := RosterEntry{PrincipalID: 1, Name: "Alpha", Kind: "AGENT", Origin: "AGENT_SPAWNED", State: "ACTIVE", CreatedAt: early, FPA: &low, Load: RosterLoad{ActiveItems: 1, Max: &maximum}}
		second := RosterEntry{PrincipalID: 2, Name: "Zulu", Kind: "HUMAN", Origin: "HUMAN_CREATED", State: "RETIRED", CreatedAt: early.Add(time.Hour), FPA: &high, Load: RosterLoad{ActiveItems: 2, Max: &maximum}}
		rows := []RosterEntry{second, first}
		require.NoError(t, sortRoster(rows, order))
		require.Equal(t, guid.ID(1), rows[0].PrincipalID, order)
		require.NoError(t, sortRoster(rows, "-"+order))
		require.Equal(t, guid.ID(2), rows[0].PrincipalID, order)
	}
}

func TestRosterPublicReadUsesCurrentSourcesAndDefaultsToDeterministicNameOrder(t *testing.T) {
	m, _, _, _ := agentHTTPHarness(t)
	_, err := m.rosterHTTP(agentContext(t, 2), &RosterInput{TeamPath: TeamPath{TenantID: "1"}, Limit: 50})
	require.Error(t, err)
	_, err = m.rosterHTTP(agentContext(t, 2), &RosterInput{TeamPath: TeamPath{TenantID: "2"}, Limit: 50})
	require.Error(t, err)
	rows := []RosterEntry{{PrincipalID: 2, Name: "same"}, {PrincipalID: 1, Name: "same"}}
	require.NoError(t, sortRoster(rows, ""))
	require.EqualValues(t, 1, rows[0].PrincipalID)
}
