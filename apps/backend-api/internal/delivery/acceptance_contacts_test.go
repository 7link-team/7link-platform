package delivery

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReleaseNoteDrafterRequiresActiveCurrentProjectRole(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
	}
	m.agents.ActiveByRoleOn = func(context.Context, *gorm.DB, guid.ID, string) ([]fleet.AgentIdentity, error) {
		return []fleet.AgentIdentity{{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}}, nil
	}
	var chosen guid.ID
	err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		chosen, e = m.selectReleaseNoteDrafterOn(ctx, tx, 1, 99)
		return e
	})
	require.NoError(t, err)
	require.EqualValues(t, 11, chosen)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseNoteWriterOn(ctx, tx, 1, 99) }), ErrWorkForbidden, "Human cannot write Agent output")
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseNoteWriterOn(ctx, tx, 1, 99) }))
	m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return false, nil }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseNoteWriterOn(ctx, tx, 1, 99) }), ErrWorkForbidden)
}

func TestReleaseNoteDrafterSourceFailuresRemainExplicit(t *testing.T) {
	for _, fault := range []string{"missing", "agent", "inactive", "member", "nonmember", "policy", "scope", "project", "role_missing", "role", "role_denied", "lead"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			injected := errors.New("current drafter facts unavailable")
			want := injected
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
			}
			switch fault {
			case "missing":
				m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			case "agent":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, injected
				}
			case "inactive":
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "RETIRED"}, true, nil
				}
				want = ErrWorkForbidden
			case "member":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, injected })
			case "nonmember":
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil })
				want = ErrWorkForbidden
			case "policy":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, injected
				}
			case "scope":
				m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					return authz.DataConstraint{}, nil
				}
				want = ErrWorkForbidden
			case "project":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("drafter-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Projects.String() {
						tx.AddError(injected)
					}
				}))
			case "role_missing":
				m.modeGovernance.AgentRoleOn = nil
				want = ErrUnavailable
			case "role":
				m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) {
					return false, injected
				}
			case "role_denied":
				m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return false, nil }
				want = ErrWorkForbidden
			case "lead":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("lead_agent_id", 33).Error)
				m.modeGovernance.AgentRoleOn = nil
				want = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.releaseNoteDrafterOn(ctx, tx, 1, 99, 11) })
			if want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, want)
			}
		})
	}
}

func TestRecordedAcceptanceContactSourceFailuresAndBoundedRoster(t *testing.T) {
	for _, fault := range []string{"missing", "query", "source", "overflow", "agent"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			injected := errors.New("contact facts unavailable")
			want := injected
			m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
				return nil, injected
			}
			switch fault {
			case "missing":
				m.sources.ContactsOn = nil
				want = ErrUnavailable
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("contact-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Contacts.String() {
						tx.AddError(injected)
					}
				}))
			case "overflow":
				at := time.Now().UTC()
				rows := []map[string]any{}
				for i := 0; i < 21; i++ {
					rows = append(rows, map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 100 + i, "role": "CC", "can_accept": true, "receives_deliveries": true, "created_at": at, "updated_at": at})
				}
				require.NoError(t, db.Table(m.service.names.Contacts.String()).Create(rows).Error)
				want = ErrUnavailable
			case "agent":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
				}
				want = ErrWorkForbidden
			}
			var id guid.ID
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				id, e = m.recordedAcceptanceContactOn(ctx, tx, 1, 99, 40)
				return e
			})
			require.ErrorIs(t, err, want)
			require.Zero(t, id)
		})
	}
}

func TestRecordedAcceptanceContactRequiresCurrentHumanAndConfiguredClient(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	principal := guid.ID(40)
	m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
		return []intake.ContactReference{{ID: 4, TenantID: 1, PrincipalID: &principal, State: "ACTIVE"}}, nil
	}
	at := time.Now().UTC()
	require.NoError(t, db.Table(m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 4, "role": "CC", "can_accept": true, "receives_deliveries": true, "created_at": at, "updated_at": at}).Error)
	read := func(tenant, project, recipient guid.ID) (guid.ID, error) {
		var id guid.ID
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			id, err = m.recordedAcceptanceContactOn(ctx, tx, tenant, project, recipient)
			return err
		})
		return id, err
	}
	id, err := read(1, 99, 40)
	require.NoError(t, err)
	require.EqualValues(t, 4, id)
	_, err = read(2, 99, 40)
	require.Error(t, err)
	_, err = read(1, 98, 40)
	require.Error(t, err)
	_, err = read(1, 99, 41)
	require.Error(t, err)
	require.NoError(t, db.Table(m.service.names.Contacts.String()).Where("project_id=99").Update("can_accept", false).Error)
	_, err = read(1, 99, 40)
	require.Error(t, err)
}

func TestReleaseNoteSelectionRechecksLeadAndFallsBackOnlyToEligibleCoordinators(t *testing.T) {
	for _, scenario := range []string{"lead", "lead_lookup", "lead_authority", "lead_ineligible", "coordinator_lookup", "coordinator_authority", "none", "missing", "agent_caller"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx := projectModeHarness(t)
			failure := errors.New("current release-note selection unavailable")
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("lead_agent_id", 33).Error)
			m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, id guid.ID) (fleet.AgentIdentity, bool, error) {
				if scenario == "lead_authority" {
					return fleet.AgentIdentity{}, false, failure
				}
				if scenario == "coordinator_authority" && id == 12 {
					return fleet.AgentIdentity{}, false, failure
				}
				state := "ACTIVE"
				if scenario == "lead_ineligible" && id == 11 {
					state = "RETIRED"
				}
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: id, State: state}, true, nil
			}
			m.triageAgents.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if scenario == "lead_lookup" {
					return fleet.AgentIdentity{}, false, failure
				}
				if scenario != "lead" && scenario != "lead_ineligible" && scenario != "lead_authority" {
					return fleet.AgentIdentity{}, false, nil
				}
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 11, State: "ACTIVE"}, true, nil
			}
			m.agents.ActiveByRoleOn = func(_ context.Context, _ *gorm.DB, _ guid.ID, role string) ([]fleet.AgentIdentity, error) {
				require.Equal(t, "COORDINATOR", role)
				if scenario == "coordinator_lookup" {
					return nil, failure
				}
				if scenario == "none" {
					return nil, nil
				}
				return []fleet.AgentIdentity{{ID: 34, TenantID: 1, PrincipalID: 12, State: "ACTIVE"}}, nil
			}
			if scenario == "missing" {
				m.agents.ActiveByRoleOn = nil
			}
			if scenario == "agent_caller" {
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			}
			var id guid.ID
			e := db.Transaction(func(tx *gorm.DB) error { var e error; id, e = m.selectReleaseNoteDrafterOn(ctx, tx, 1, 99); return e })
			switch scenario {
			case "lead":
				require.NoError(t, e)
				require.Equal(t, guid.ID(11), id)
			case "lead_ineligible":
				require.NoError(t, e)
				require.Equal(t, guid.ID(12), id)
			case "none", "missing":
				require.ErrorIs(t, e, ErrUnavailable)
				require.Zero(t, id)
			case "agent_caller":
				require.ErrorIs(t, e, ErrWorkForbidden)
				require.Zero(t, id)
			default:
				require.ErrorIs(t, e, failure)
				require.Zero(t, id)
			}
		})
	}
}
