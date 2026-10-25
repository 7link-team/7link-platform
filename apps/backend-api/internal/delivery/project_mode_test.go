package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModeTakeoverSourceFailuresRollBackProofRevocationAndVersion(t *testing.T) {
	for _, fault := range []string{"authority", "revoke_missing", "revoke", "spawn_missing", "spawn", "update", "id", "history", "event"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			injected := errors.New("mode takeover unavailable")
			want := injected
			switch fault {
			case "authority":
				m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) { return "", injected }
			case "revoke_missing":
				m.modeGovernance.RevokeOn = nil
				want = ErrUnavailable
			case "revoke":
				m.modeGovernance.RevokeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error) {
					return nil, injected
				}
			case "spawn_missing":
				m.modeGovernance.RevokeSpawnOn = nil
				want = ErrUnavailable
			case "spawn":
				m.modeGovernance.RevokeSpawnOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error) {
					return nil, injected
				}
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("mode-takeover-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(injected)
					}
				}))
			case "id":
				m.ids = unavailableActivityIDs{err: injected}
			case "history":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("mode-takeover-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.ModeChanges.String() {
						tx.AddError(injected)
					}
				}))
			case "event":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "HYBRID", Reason: "Human takes over delegated authority", ReauthProof: "proof"}, "cookie", true, []guid.ID{11})
				return e
			})
			require.ErrorIs(t, e, want)
			row, e := query.BindProjects(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			var count int64
			require.NoError(t, db.Table("mode_probe").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func projectModeHarness(t *testing.T) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	m, db, ctx, _ := stageHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE mode_probe (kind TEXT NOT NULL)").Error)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"stage": "ACTIVE", "budget_cap_micros": 1000000}).Error)
	m.modeGovernance = governance.ProjectModeGovernance{
		PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{11}, nil },
		AuthorityOn: func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string) (string, error) {
			require.Contains(t, []string{"project_mode_change", "project_takeover"}, permission)
			return "ROLE:team_owner", nil
		},
		AgentRoleOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return true, nil },
		PreconditionsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
			return true, nil, nil
		},
		RevokeOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _, _ string, _ []guid.ID) ([]guid.ID, error) {
			return []guid.ID{51}, tx.Exec("INSERT INTO mode_probe VALUES ('delegation')").Error
		},
		RevokeSpawnOn: func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _, _ string, _ []guid.ID) ([]guid.ID, error) {
			return []guid.ID{61}, tx.Exec("INSERT INTO mode_probe VALUES ('spawn')").Error
		},
	}
	m.scopeReauthentication = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.EqualValues(t, 11, actor)
		require.Equal(t, "project:99", target)
		require.Contains(t, []string{"team_mode_change", "project_takeover"}, purpose)
		if cookie != "cookie" || proof != "proof" {
			return ErrWorkForbidden
		}
		return tx.Exec("INSERT INTO mode_probe VALUES ('proof')").Error
	}
	return m, db, ctx
}

func TestProjectModeLifecyclePersistsExactRevocationFacts(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	call := func(version int, to string, takeover bool) (ProjectModeReceipt, error) {
		var out ProjectModeReceipt
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.changeProjectModeOn(ctx, tx, 1, 99, 11, version, projectModeBody{ToMode: to, Reason: "Human controls the project authority", ReauthProof: "proof"}, "cookie", takeover, []guid.ID{11})
			return err
		})
		return out, err
	}
	automatic, err := call(1, "AUTONOMOUS", false)
	require.NoError(t, err)
	require.Equal(t, "AUTO", automatic.Project.DispatchPolicy)
	require.NotNil(t, automatic.ModeChangeID)
	require.Nil(t, automatic.TakeoverID)
	require.JSONEq(t, "[]", automatic.ModeChange.RevokedSpawnGrantIDs)
	manual, err := call(2, "HYBRID", false)
	require.NoError(t, err)
	require.Equal(t, "MANUAL", manual.Project.DispatchPolicy)
	require.Equal(t, []guid.ID{51}, manual.ModeChange.RevokedIDs)
	require.Empty(t, manual.ModeChange.RevokedSpawnIDs)
	var emitted []events.Event
	m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, e events.Event) (guid.ID, error) {
		emitted = append(emitted, e)
		return 1, nil
	})
	takeover, err := call(3, "HYBRID", true)
	require.NoError(t, err)
	require.Nil(t, takeover.ModeChangeID)
	require.Equal(t, &takeover.ModeChange.ID, takeover.TakeoverID)
	require.Equal(t, []guid.ID{61}, takeover.ModeChange.RevokedSpawnIDs)
	require.Len(t, emitted, 1)
	require.Equal(t, "project.taken_over", emitted[0].Type)
	payload, ok := emitted[0].Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, takeover.ModeChange.ID.String(), payload["takeover_id"])
	require.Nil(t, payload["mode_change_id"])
	history, err := query.BindModeChanges(db, m.service.names).WithContext(ctx).Order(query.BindModeChanges(db, m.service.names).VersionAfter).Find()
	require.NoError(t, err)
	require.Len(t, history, 3)
	require.JSONEq(t, `["61"]`, history[2].RevokedSpawnGrantIDs)
}

func TestProjectModeFailuresRollbackProofRevocationAndHistory(t *testing.T) {
	for _, scenario := range []string{"stale", "proof", "criteria", "delegation", "spawn", "audit", "outbox", "closed", "authority"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("team_mode", "AUTONOMOUS").Error)
			version := 1
			proof := "proof"
			to := "HUMAN_ONLY"
			failure := errors.New("mode dependency failed")
			switch scenario {
			case "stale":
				version = 2
			case "proof":
				proof = "wrong"
			case "criteria":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("team_mode", "HYBRID").Error)
				to = "AUTONOMOUS"
				m.modeGovernance.PreconditionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
					return false, nil, nil
				}
			case "delegation":
				m.modeGovernance.RevokeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error) {
					return nil, failure
				}
			case "spawn":
				m.modeGovernance.RevokeSpawnOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) ([]guid.ID, error) {
					return nil, failure
				}
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("stage", "CLOSED").Error)
			case "authority":
				m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) { return "", ErrWorkForbidden }
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.changeProjectModeOn(ctx, tx, 1, 99, 11, version, projectModeBody{ToMode: to, Reason: "Human reviews the control authority", ReauthProof: proof}, "cookie", false, []guid.ID{11})
				return err
			})
			require.Error(t, err)
			p := query.BindProjects(db, m.service.names)
			row, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).First()
			require.NoError(t, err)
			require.Equal(t, 1, row.Version)
			count, err := query.BindModeChanges(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			var probes int64
			require.NoError(t, db.Table("mode_probe").Count(&probes).Error)
			require.Zero(t, probes)
		})
	}
}

func TestProjectTakeoverRequiresDistinctCurrentPermission(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	m.modeGovernance.AuthorityOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string) (string, error) {
		if permission == "project_takeover" {
			return "", governance.ErrForbidden
		}
		return "ROLE:team_owner", nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.projectModeAuthorizeOn(ctx, tx, 1, 99, 11, true, []guid.ID{11})
		return err
	})
	require.ErrorIs(t, err, governance.ErrForbidden)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.projectModeAuthorizeOn(ctx, tx, 1, 99, 11, false, []guid.ID{11})
		return err
	}))
}

func TestProjectModePrerequisitesFailClosedOnUnavailableAuthority(t *testing.T) {
	for _, scenario := range []string{"missing_roles", "missing_delegation", "delegation_error", "role_error", "no_executor", "missing_cap", "exhausted", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			failure := errors.New("governance unavailable")
			want := error(nil)
			switch scenario {
			case "missing_roles":
				m.modeGovernance.AgentRoleOn = nil
				want = ErrUnavailable
			case "missing_delegation":
				m.modeGovernance.PreconditionsOn = nil
				want = ErrUnavailable
			case "delegation_error":
				m.modeGovernance.PreconditionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
					return false, nil, failure
				}
				want = failure
			case "role_error":
				m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) {
					return false, failure
				}
				want = failure
			case "no_executor":
				m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return false, nil }
			case "missing_cap":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("budget_cap_micros", nil).Error)
			case "exhausted":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("budget_state", "EXHAUSTED").Error)
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("stage", "CLOSED").Error)
			}
			q := query.BindProjects(db, m.service.names)
			p, err := q.WithContext(ctx).First()
			require.NoError(t, err)
			criteria, err := m.modePreconditionsOn(ctx, db, p, []guid.ID{11})
			if want != nil {
				require.ErrorIs(t, err, want)
				return
			}
			require.NoError(t, err)
			require.Len(t, criteria, 5)
			failed := []string{}
			for _, c := range criteria {
				if !c.Passed {
					failed = append(failed, c.Key)
				}
			}
			expected := "P3"
			if scenario == "no_executor" {
				expected = "P4"
			}
			if scenario == "closed" {
				expected = "P5"
			}
			require.Equal(t, []string{expected}, failed)
		})
	}
}

func TestProjectHumanOnlyTransitionRevokesAuthorityAndTakeoverPreservesMode(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("team_mode", "AUTONOMOUS").Error)
	var human ProjectModeReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		human, err = m.changeProjectModeOn(ctx, tx, 1, 99, 11, 1, projectModeBody{ToMode: "HUMAN_ONLY", Reason: "Human team resumes all decision authority", ReauthProof: "proof"}, "cookie", false, []guid.ID{11})
		return err
	}))
	require.Equal(t, "HUMAN_ONLY", human.Project.TeamMode)
	require.Equal(t, "MANUAL", human.Project.DispatchPolicy)
	require.Equal(t, []guid.ID{51}, human.ModeChange.RevokedIDs)
	require.Equal(t, []guid.ID{61}, human.ModeChange.RevokedSpawnIDs)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeProjectModeOn(ctx, tx, 1, 99, 11, 2, projectModeBody{ToMode: "AUTONOMOUS", Reason: "Request autonomous work from Human mode", ReauthProof: "proof"}, "cookie", false, []guid.ID{11})
		return err
	})
	require.ErrorIs(t, err, ErrModeIllegal)
	var takeover ProjectModeReceipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		takeover, err = m.changeProjectModeOn(ctx, tx, 1, 99, 11, 2, projectModeBody{ToMode: "HYBRID", Reason: "Human retains all existing project control", ReauthProof: "proof"}, "cookie", true, []guid.ID{11})
		return err
	}))
	require.Equal(t, "HUMAN_ONLY", takeover.Project.TeamMode)
	require.Equal(t, human.Project.TeamModeChangedAt, takeover.Project.TeamModeChangedAt)
	require.Nil(t, takeover.ModeChangeID)
	require.NotNil(t, takeover.TakeoverID)
}

func TestProjectModeCommandRejectsCurrentAuthorityAndStorageBoundaryFailures(t *testing.T) {
	for _, fault := range []string{"authority_missing", "authority_empty", "proof_missing", "proof", "closed", "version", "takeover_mode", "holds", "preconditions", "audit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			failure := errors.New("current mode facts unavailable")
			want := ErrUnavailable
			version := 1
			takeover := false
			in := projectModeBody{ToMode: "AUTONOMOUS", Reason: "Current Human authorizes project automation", ReauthProof: "proof"}
			switch fault {
			case "authority_missing":
				m.modeGovernance.AuthorityOn = nil
			case "authority_empty":
				m.modeGovernance.AuthorityOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (string, error) { return "", nil }
				want = ErrWorkForbidden
			case "proof_missing":
				m.scopeReauthentication = nil
			case "proof":
				in.ReauthProof = "invalid"
				want = ErrWorkForbidden
			case "closed":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("stage", "CLOSED").Error)
				want = ErrWorkProjectReadOnly
			case "version":
				version = 9
				want = ErrWorkVersion
			case "takeover_mode":
				takeover = true
				want = ErrInvalidArgument
			case "holds":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=99").Update("holds", "{}").Error)
			case "preconditions":
				m.modeGovernance.PreconditionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (bool, []string, error) {
					return false, nil, failure
				}
				want = failure
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
				want = failure
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeProjectModeOn(ctx, tx, 1, 99, 11, version, in, "cookie", takeover, []guid.ID{11})
				return e
			})
			require.ErrorIs(t, e, want)
			row, e := query.BindProjects(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			require.Equal(t, "HYBRID", row.TeamMode)
			var n int64
			require.NoError(t, db.Table("mode_probe").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
