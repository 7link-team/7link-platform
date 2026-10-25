package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestRepositoryReplacementFailuresRetainStoredIdentity(t *testing.T) {
	for _, fault := range []string{"read", "id", "create", "update", "archive", "blank", "branch", "limit"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			injected := errors.New("repository settings unavailable")
			want := injected
			row, e := query.BindProjects(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			call := func(repos []ProjectRepositorySetting) error {
				return db.Transaction(func(tx *gorm.DB) error {
					return m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{Repositories: &repos})
				})
			}
			repo := ProjectRepositorySetting{Name: "primary", RemoteURL: "https://example.test/repo.git", DefaultBranch: "main", ProtectedBranches: []string{"main"}, IsPrimary: true}
			require.NoError(t, call([]ProjectRepositorySetting{repo}))
			before, e := query.BindRepositories(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			repos := []ProjectRepositorySetting{repo}
			switch fault {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("repository-settings-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Repositories.String() {
						tx.AddError(injected)
					}
				}))
			case "id":
				m.ids = unavailableActivityIDs{err: injected}
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("repository-settings-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Repositories.String() {
						tx.AddError(injected)
					}
				}))
			case "update", "archive":
				if fault == "update" {
					repos[0].ID = &before.ID
					repos[0].DefaultBranch = "changed"
				} else {
					repos = nil
				}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("repository-settings-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Repositories.String() {
						tx.AddError(injected)
					}
				}))
			case "blank":
				repos[0].Name = " "
				want = ErrInvalidArgument
			case "branch":
				repos[0].ProtectedBranches = []string{" "}
				want = ErrInvalidArgument
			case "limit":
				repos = make([]ProjectRepositorySetting, 11)
				want = ErrInvalidArgument
			}
			require.ErrorIs(t, call(repos), want)
			require.NoError(t, db.Callback().Query().Remove("repository-settings-failure"))
			after, e := query.BindRepositories(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestRemovingCurrentLeadClearsDispatchOnlyAfterCurrentSourceChecks(t *testing.T) {
	for _, fault := range []string{"ok", "source", "missing", "delete", "bindings", "role", "assignments", "event"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			injected := errors.New("member removal unavailable")
			want := injected
			m.roles = governance.SeededRoles{RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) {
				if fault == "role" {
					return 0, injected
				}
				return 700, nil
			}}
			m.settingsMembers = core.ProjectMemberBindings{ListOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
				if fault == "bindings" {
					return nil, injected
				}
				return []core.EntityRoleBinding{{RoleID: 700, PrincipalID: 12, Effect: core.BindingAllow, ExpiresAt: time.Now().UTC().Add(time.Hour)}}, nil
			}, DeleteOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
				if fault == "delete" {
					return false, injected
				}
				return true, nil
			}}
			m.service.roles.PutOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, core.BindingEffect, time.Time) (core.EntityRoleBinding, bool, error) {
				t.Fatal("removal must not grant")
				return core.EntityRoleBinding{}, false, nil
			}
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "source" {
					return fleet.AgentIdentity{}, false, injected
				}
				return fleet.AgentIdentity{ID: 33}, true, nil
			}
			if fault == "missing" {
				m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			}
			if fault == "assignments" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("remove-member-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.service.names.Assignments.String() {
						tx.AddError(injected)
					}
				}))
			}
			if fault == "event" {
				m.service.events = nil
				want = ErrUnavailable
			}
			var row *model.Project
			e := db.Transaction(func(tx *gorm.DB) error {
				var e error
				row, e = query.BindProjects(tx, m.service.names).WithContext(ctx).Take()
				if e != nil {
					return e
				}
				lead := guid.ID(33)
				row.LeadAgentID = &lead
				row.DispatchPolicy = "AUTO"
				return m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{RemoveMembers: []ProjectMemberSetting{{11, "delivery_lead"}}})
			})
			if fault == "ok" {
				require.NoError(t, e)
				require.Nil(t, row.LeadAgentID)
				require.Equal(t, "MANUAL", row.DispatchPolicy)
				require.True(t, row.DispatchPolicyExplicit)
			} else {
				require.ErrorIs(t, e, want)
			}
		})
	}
}

func TestSettingsDetailsExposeCurrentContactsAndExpiryAndRejectCorruptSources(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	expires := time.Now().UTC().Add(time.Hour)
	m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return []core.EntityRoleBinding{{EntityID: 99, RoleID: 700, PrincipalID: 11, Effect: core.BindingAllow, ExpiresAt: expires}}, nil
	}
	require.NoError(t, db.Table(m.service.names.Contacts.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "intake_contact_id": 401, "role": "CC", "can_accept": false, "receives_deliveries": true, "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
	var out ClientSettings
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.settingsDetailsOn(ctx, tx, 1, 99, &out) }))
	require.Len(t, out.Contacts, 1)
	require.True(t, *out.Contacts[0].ReceivesDeliveries)
	require.Len(t, out.Members, 1)
	require.True(t, expires.Equal(*out.Members[0].ExpiresAt))
	injected := errors.New("settings read unavailable")
	for _, table := range []string{m.service.names.Contacts.String(), m.service.names.Repositories.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("settings-read-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.settingsDetailsOn(ctx, tx, 1, 99, &out) }), injected)
		require.NoError(t, db.Callback().Query().Remove("settings-read-failure"))
	}
	m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return nil, injected
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.settingsDetailsOn(ctx, tx, 1, 99, &out) }), injected)
}

func TestProjectSettingsRepositoriesRetainIdentityArchiveAndRollback(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	m.service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
		return core.Entity{Name: "Project"}, nil
	}
	m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return []core.EntityRoleBinding{}, nil
	}
	adapter := clientWork{m: m}
	call := func(v int, repos []ProjectRepositorySetting, abort bool) (ClientSettings, error) {
		var out ClientSettings
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = adapter.SettingsOn(ctx, tx, 1, 99, &ClientSettingsPatch{Repositories: &repos}, v)
			if e != nil {
				return e
			}
			if abort {
				return errors.New("outbox failed")
			}
			return nil
		})
		return out, e
	}
	repos := []ProjectRepositorySetting{{Name: "Source", RemoteURL: "https://example.test/repo.git", DefaultBranch: "main", ProtectedBranches: []string{"main"}, IsPrimary: true}}
	first, e := call(1, repos, false)
	require.NoError(t, e)
	require.Len(t, first.Repositories, 1)
	id := *first.Repositories[0].ID
	repos = first.Repositories
	repos[0].DefaultBranch = "release"
	next, e := call(2, repos, false)
	require.NoError(t, e)
	require.Equal(t, id, *next.Repositories[0].ID)
	_, e = call(3, []ProjectRepositorySetting{}, true)
	require.Error(t, e)
	q := query.BindRepositories(db, m.service.names)
	rows, e := q.WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].ArchivedAt)
	_, e = call(3, []ProjectRepositorySetting{}, false)
	require.NoError(t, e)
	rows, e = q.WithContext(ctx).Find()
	require.NoError(t, e)
	require.NotNil(t, rows[0].ArchivedAt)
	require.Equal(t, id, rows[0].ID)
	_, e = call(4, repos, false)
	require.ErrorIs(t, e, ErrInvalidArgument, "archived identities cannot be silently revived")
	foreign := guid.ID(100000)
	repos[0].ID = &foreign
	_, e = call(4, repos, false)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestProjectSettingsContactReplacementValidatesAndRollsBack(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	yes, no := true, false
	m.sources = intake.IntakeReader{GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
		return intake.IntakeRef{}, false, nil
	}, ContactsOn: func(_ context.Context, _ *gorm.DB, tenant guid.ID, ids []guid.ID) ([]intake.ContactReference, error) {
		rows := []intake.ContactReference{}
		for _, id := range ids {
			rows = append(rows, intake.ContactReference{TenantID: tenant, ID: id})
		}
		return rows, nil
	}}
	call := func(contacts []projectContactInput) error {
		return db.Transaction(func(tx *gorm.DB) error {
			row, e := query.BindProjects(tx, m.service.names).WithContext(ctx).Take()
			if e != nil {
				return e
			}
			return m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{Contacts: &contacts})
		})
	}
	contacts := []projectContactInput{{IntakeContactID: 4, Role: "PRIMARY", CanAccept: &yes, ReceivesDeliveries: &no}, {IntakeContactID: 5, Role: "CC", CanAccept: &no, ReceivesDeliveries: &yes}}
	require.NoError(t, call(contacts))
	rows, e := query.BindContacts(db, m.service.names).WithContext(ctx).Order(query.BindContacts(db, m.service.names).IntakeContactID).Find()
	require.NoError(t, e)
	require.Len(t, rows, 2)
	require.True(t, rows[0].CanAccept)
	require.False(t, rows[0].ReceivesDeliveries)
	require.Equal(t, guid.ID(11), *rows[0].CreatedBy)
	invalid := []projectContactInput{{IntakeContactID: 4, Role: "PRIMARY", CanAccept: &no, ReceivesDeliveries: &yes}}
	require.ErrorIs(t, call(invalid), ErrInvalidArgument)
	invalid = append(contacts, contacts[0])
	require.ErrorIs(t, call(invalid), ErrInvalidArgument)
	invalid = []projectContactInput{{IntakeContactID: 4, Role: "CC"}}
	require.ErrorIs(t, call(invalid), ErrInvalidArgument)
	require.ErrorIs(t, call(make([]projectContactInput, 21)), ErrInvalidArgument)
	injected := errors.New("contact replacement unavailable")
	for _, operation := range []string{"delete", "create", "source"} {
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == m.service.names.Contacts.String() {
				tx.AddError(injected)
			}
		}
		switch operation {
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("settings-failure", callback))
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("settings-failure", callback))
		case "source":
			m.sources.ContactsOn = func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
				return nil, injected
			}
		}
		require.ErrorIs(t, call(contacts), injected)
		require.NoError(t, db.Callback().Delete().Remove("settings-failure"))
		require.NoError(t, db.Callback().Create().Remove("settings-failure"))
		after, e := query.BindContacts(db, m.service.names).WithContext(ctx).Order(query.BindContacts(db, m.service.names).IntakeContactID).Find()
		require.NoError(t, e)
		require.Equal(t, rows, after)
	}
}

func TestProjectSettingsMemberAdditionUsesCurrentBindingsAndEmitsAtomically(t *testing.T) {
	for _, fault := range []string{"", "missing", "list", "deny", "put", "event"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			injected := errors.New("member binding unavailable")
			want := injected
			m.roles = governance.SeededRoles{RoleIDOn: func(_ context.Context, _ *gorm.DB, _ guid.ID, key string) (guid.ID, error) {
				if key == "engineer" {
					return 701, nil
				}
				return 700, nil
			}}
			m.settingsMembers = core.ProjectMemberBindings{ListOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
				if fault == "list" {
					return nil, injected
				}
				if fault == "deny" {
					return []core.EntityRoleBinding{{PrincipalID: 11, RoleID: 701, Effect: core.BindingDeny}}, nil
				}
				return nil, nil
			}, DeleteOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
				t.Fatal("addition cannot delete")
				return false, nil
			}}
			require.NoError(t, db.Exec("CREATE TABLE member_effects(kind TEXT)").Error)
			m.service.roles.PutOn = func(_ context.Context, tx *gorm.DB, tenant, project, role, principal guid.ID, effect core.BindingEffect, _ time.Time) (core.EntityRoleBinding, bool, error) {
				require.Equal(t, guid.ID(701), role)
				require.Equal(t, guid.ID(11), principal)
				require.Equal(t, core.BindingAllow, effect)
				if fault == "put" {
					return core.EntityRoleBinding{}, false, injected
				}
				return core.EntityRoleBinding{}, true, tx.Exec("INSERT INTO member_effects VALUES ('grant')").Error
			}
			m.service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
				require.Equal(t, "project.members_changed", e.Type)
				if fault == "event" {
					return 0, injected
				}
				return 1, tx.Exec("INSERT INTO member_effects VALUES ('event')").Error
			})
			if fault == "missing" {
				m.settingsMembers.ListOn = nil
				want = ErrUnavailable
			}
			if fault == "deny" {
				want = ErrWorkForbidden
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				row, e := query.BindProjects(tx, m.service.names).WithContext(ctx).Take()
				if e != nil {
					return e
				}
				return m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{AddMembers: []ProjectMemberSetting{{11, "engineer"}}})
			})
			var count int64
			require.NoError(t, db.Table("member_effects").Count(&count).Error)
			if fault == "" {
				require.NoError(t, err)
				require.EqualValues(t, 2, count)
			} else {
				require.ErrorIs(t, err, want)
				require.Zero(t, count)
			}
		})
	}
}

func TestProjectSettingsLeadMustRemainCurrentAndCanBeClearedOnlyOutsideAutonomy(t *testing.T) {
	for _, fault := range []string{"", "missing", "source", "kind", "member", "inactive", "role", "denied", "clear", "autonomous"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			injected := errors.New("lead facts unavailable")
			lead := guid.ID(11)
			want := injected
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				if fault == "source" {
					return fleet.AgentIdentity{}, false, injected
				}
				roles := []string{"LEAD"}
				if fault == "kind" {
					roles = nil
				}
				return fleet.AgentIdentity{ID: 33, PrincipalID: 11, TenantID: 1, State: "ACTIVE", Roles: roles}, true, nil
			}
			m.modeGovernance.AgentRoleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) {
				if fault == "role" {
					return false, injected
				}
				return fault != "denied", nil
			}
			if fault == "missing" {
				m.agents.ByPrincipalOn = nil
				want = ErrUnavailable
			}
			if fault == "kind" || fault == "inactive" || fault == "denied" {
				want = ErrInvalidMember
			}
			if fault == "member" || fault == "inactive" {
				m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
					if fault == "member" {
						return false, injected
					}
					return false, nil
				})
			}
			if fault == "clear" || fault == "autonomous" {
				lead = 0
			}
			if fault == "autonomous" {
				want = ErrModePreconditions
			}
			var row *model.Project
			err := db.Transaction(func(tx *gorm.DB) error {
				var e error
				row, e = query.BindProjects(tx, m.service.names).WithContext(ctx).Take()
				if e != nil {
					return e
				}
				if fault == "autonomous" {
					row.TeamMode = "AUTONOMOUS"
				}
				return m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{LeadPrincipalID: &lead})
			})
			if fault == "" {
				require.NoError(t, err)
				require.Equal(t, guid.ID(33), *row.LeadAgentID)
			} else if fault == "clear" {
				require.NoError(t, err)
				require.Nil(t, row.LeadAgentID)
			} else {
				require.ErrorIs(t, err, want)
			}
		})
	}
	var empty *ClientSettingsPatch
	require.Empty(t, empty.MemberPrincipals())
	lead := guid.ID(12)
	in := &ClientSettingsPatch{LeadPrincipalID: &lead, AddMembers: []ProjectMemberSetting{{11, "engineer"}, {12, "engineer"}}, RemoveMembers: []ProjectMemberSetting{{11, "engineer"}}}
	require.Equal(t, []guid.ID{11, 12}, in.MemberPrincipals())
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, module.Provide(s, core.ProjectMemberBindings{ListOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) { return nil, nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectSettingsMembers(s))
	require.NotNil(t, m.settingsMembers.ListOn)
}
func TestProjectSettingsDispatchAndVisibilityValidateWithoutModeBypass(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	q := query.BindProjects(db, m.service.names)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, e := q.WithContext(ctx).First()
		require.NoError(t, e)
		row.TeamMode = "HUMAN_ONLY"
		auto := "AUTO"
		require.ErrorIs(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{DispatchPolicy: &auto}), ErrInvalidArgument)
		yes, no := true, false
		portal := projectPortalInput{ShowAILabel: &no, ShowWorkItemTitles: &yes, ShowEvidenceSummary: &no, ShowReleaseNotes: &yes, MilestonesVisible: &no, ContributorDisplay: "ROLES"}
		manual := "MANUAL"
		require.NoError(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{DispatchPolicy: &manual, PortalSettings: &portal}))
		require.True(t, row.DispatchPolicyExplicit)
		require.Contains(t, row.PortalSettings, `"show_ai_label":false`)
		portal.ContributorDisplay = "EVERYTHING"
		require.ErrorIs(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{PortalSettings: &portal}), ErrInvalidArgument)
		return nil
	}))
}

func TestProjectSettingsMemberRemovalGuardsLastLeadAndActiveAssignment(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, false)
	assignmentClaim(t, m, db, ctx, work, true)
	m.roles = governance.SeededRoles{RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 700, nil }}
	m.settingsMembers = core.ProjectMemberBindings{ListOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		return []core.EntityRoleBinding{{RoleID: 700, PrincipalID: 11, Effect: core.BindingAllow}}, nil
	}, DeleteOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
		t.Fatal("guard must precede deletion")
		return false, nil
	}}
	m.service.roles.PutOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, core.BindingEffect, time.Time) (core.EntityRoleBinding, bool, error) {
		t.Fatal("remove must not grant")
		return core.EntityRoleBinding{}, false, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		p := query.BindProjects(tx, m.service.names)
		row, e := p.WithContext(ctx).First()
		require.NoError(t, e)
		require.ErrorIs(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{RemoveMembers: []ProjectMemberSetting{{11, "engineer"}}}), ErrInvalidArgument)
		a := query.BindAssignments(tx, m.service.names)
		_, e = a.WithContext(ctx).Where(a.TenantID.Eq(guid.ID(1)), a.ProjectID.Eq(guid.ID(99))).UpdateSimple(a.Status.Value("RELEASED"), a.EndReason.Value("Reassigned before removal"), a.EndedAt.Value(time.Now().UTC()), a.ActiveSlot.Null(), a.LeaseExpiresAt.Null())
		require.NoError(t, e)
		require.ErrorIs(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{RemoveMembers: []ProjectMemberSetting{{11, "delivery_lead"}}}), ErrInvalidArgument)
		require.ErrorIs(t, m.editSettingsDetailsOn(ctx, tx, row, &ClientSettingsPatch{RemoveMembers: []ProjectMemberSetting{{12, "delegated_project_manage"}}}), ErrInvalidMember)
		return nil
	}))
}
