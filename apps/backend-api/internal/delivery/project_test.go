package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type projectPublisher func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f projectPublisher) Publish(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
	return f(ctx, tx, event)
}

func projectHarness(t *testing.T) (*projectService, *gorm.DB, projectDraft) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "project.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = "d_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	require.NoError(t, db.Exec("CREATE TABLE provision_probe (step TEXT NOT NULL)").Error)
	now := time.Now().UTC().Truncate(time.Millisecond)
	draft := projectDraft{
		entity:       core.Entity{TenantID: 1, Type: "project", Name: "Project", Status: core.EntityActive},
		project:      model.Project{TenantID: 1, Code: "AB", Stage: "INTAKE", StageChangedAt: now, TeamMode: "HYBRID", DispatchPolicy: "MANUAL", EnsembleMaxBranches: 3, BudgetCapVersion: 1, BudgetNotifyPct: 80, BudgetState: "NORMAL", Holds: "[]", Health: "GREEN", HealthReasons: "[]", PortalSettings: "{}", AcceptancePolicy: "{}", NextDeliverySeq: 1, ProvisioningIssues: "[]", Version: 1, CreatedAt: now, UpdatedAt: now},
		contacts:     []model.ProjectContact{{TenantID: 1, IntakeContactID: 4, Role: "PRIMARY", ReceivesDeliveries: true, CanAccept: true, CreatedAt: now, UpdatedAt: now}},
		repositories: []model.ProjectRepository{{TenantID: 1, ID: 5, Name: "Source", RemoteURL: "https://example.test/repo.git", DefaultBranch: "main", ProtectedBranches: "[]", Version: 1, CreatedAt: now, UpdatedAt: now}},
		members:      []projectMember{{principalID: 11, roleID: 6}},
		actor:        events.Actor{Kind: "HUMAN", ID: 11, AuthoritySource: "ROLE:team_owner"},
		audit:        collab.ProvisioningAudit{AuthoritySource: "ROLE:team_owner"},
	}
	return &projectService{names: query.BindNames("d_")}, db, draft
}

func TestProjectCreationCommitsOrRollsBackEveryStep(t *testing.T) {
	for _, failure := range []string{"", "client", "bad_client", "entity", "project", "contact", "repository", "binding", "channel", "audit", "event", "invalid_tenant", "invalid_member", "missing_creator", "foreign_contact", "foreign_repository", "bad_entity", "bad_channel"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			service, db, draft := projectHarness(t)
			clientName := "Client"
			draft.client = &projectClientInput{NewName: &clientName}
			injected := errors.New("injected provisioning failure")
			validationFailure := false
			switch failure {
			case "invalid_tenant":
				draft.project.TenantID = 2
				validationFailure = true
			case "invalid_member":
				draft.members[0].roleID = 0
				validationFailure = true
			case "missing_creator":
				draft.members = nil
				validationFailure = true
			case "foreign_contact":
				draft.contacts[0].TenantID = 2
				validationFailure = true
			case "foreign_repository":
				draft.repositories[0].TenantID = 2
				validationFailure = true
			}
			ctx := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 1, PrincipalID: 11})
			var active *gorm.DB
			write := func(tx *gorm.DB, step string) error {
				require.Same(t, active, tx)
				if err := tx.Exec("INSERT INTO provision_probe VALUES (?)", step).Error; err != nil {
					return err
				}
				if step == failure {
					return injected
				}
				return nil
			}
			for stage, table := range map[string]string{"project": "d_delivery_projects", "contact": "d_delivery_project_contacts", "repository": "d_delivery_project_repositories"} {
				if stage == failure {
					require.NoError(t, db.Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'injected provisioning failure'); END").Error)
				}
			}
			service.entities.CreateOn = func(_ context.Context, tx *gorm.DB, entity core.Entity) (core.Entity, error) {
				if entity.Type == "client" {
					entity.ID = 98
					if failure == "bad_client" {
						entity.Type = "project"
					}
					return entity, write(tx, "client")
				}
				require.Equal(t, guid.ID(98), entity.ParentID)
				entity.ID = 99
				if failure == "bad_entity" {
					entity.TenantID = 2
				}
				return entity, write(tx, "entity")
			}
			service.roles.PutOn = func(_ context.Context, tx *gorm.DB, tenant, entity, role, principal guid.ID, effect core.BindingEffect, expiry time.Time) (core.EntityRoleBinding, bool, error) {
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(99), entity)
				require.Equal(t, guid.ID(6), role)
				require.Equal(t, guid.ID(11), principal)
				require.Equal(t, core.BindingAllow, effect)
				require.True(t, expiry.IsZero())
				return core.EntityRoleBinding{EntityID: entity, RoleID: role, PrincipalID: principal}, true, write(tx, "binding")
			}
			service.channels.CreateDefaultOn = func(_ context.Context, tx *gorm.DB, in collab.DefaultChannelInput) (guid.ID, error) {
				require.Equal(t, guid.ID(99), in.ProjectID)
				require.Equal(t, []guid.ID{11}, in.MemberPrincipalIDs)
				require.Equal(t, draft.audit, in.Audit)
				if failure == "bad_channel" {
					return 0, nil
				}
				return 8, write(tx, "channel")
			}
			service.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				require.Equal(t, "delivery.project_created", event.EventType)
				require.Equal(t, guid.ID(11), event.PrincipalID)
				return write(tx, "audit")
			}
			service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
				require.Equal(t, "project.created", event.Type)
				require.Equal(t, int64(1), event.SubjectVersion)
				require.Equal(t, guid.ID(99), event.ProjectID)
				require.Equal(t, draft.actor, event.Actor)
				return 9, write(tx, "event")
			})
			var result projectCreated
			err := db.Transaction(func(tx *gorm.DB) error {
				active = tx
				var err error
				result, err = service.createOn(ctx, tx, draft)
				return err
			})
			if failure != "" {
				if validationFailure {
					require.ErrorIs(t, err, ErrInvalidArgument)
				} else if failure == "bad_entity" || failure == "bad_channel" || failure == "bad_client" {
					require.ErrorIs(t, err, ErrUnavailable)
				} else {
					require.ErrorContains(t, err, injected.Error())
				}
				require.Zero(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, guid.ID(99), result.project.ProjectID)
				require.Equal(t, guid.ID(98), *result.project.ClientEntityID)
				require.Equal(t, guid.ID(98), result.entity.ParentID)
				require.Equal(t, guid.ID(8), result.channelID)
				require.Len(t, result.bindings, 1)
			}
			for _, table := range []string{"d_delivery_projects", "d_delivery_project_contacts", "d_delivery_project_repositories", "provision_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				if failure != "" {
					require.Zero(t, count, table)
				} else if table == "provision_probe" {
					require.EqualValues(t, 6, count)
				} else {
					require.EqualValues(t, 1, count, table)
				}
			}
			require.Zero(t, draft.project.ProjectID)
			require.Zero(t, draft.contacts[0].ProjectID)
			require.Zero(t, draft.repositories[0].ProjectID)
		})
	}
}

func TestProjectCreationRequiresCallerTransactionAndCapabilities(t *testing.T) {
	service, db, draft := projectHarness(t)
	_, err := service.createOn(t.Context(), nil, draft)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = service.createOn(t.Context(), db, draft)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := service.createOn(t.Context(), tx, draft)
		require.ErrorIs(t, err, ErrUnavailable)
		_, err = (*projectService)(nil).createOn(t.Context(), tx, draft)
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
}

func TestProjectClientResolutionUsesCurrentTenantTransaction(t *testing.T) {
	for _, scenario := range []string{"none", "selected", "internal_parent", "missing", "disabled", "wrong_type", "wrong_tenant", "wrong_id", "read_failure", "no_reader", "ambiguous", "empty_choice", "empty_name", "zero_id", "both_choices"} {
		t.Run(scenario, func(t *testing.T) {
			service, db, _ := projectHarness(t)
			id := guid.ID(51)
			input := &projectClientInput{EntityID: &id}
			parent := guid.ID(0)
			var expected error
			calls := 0
			switch scenario {
			case "none":
				input = nil
			case "internal_parent":
				input = nil
				parent = id
			case "missing", "disabled", "wrong_type", "wrong_tenant", "wrong_id", "zero_id":
				expected = ErrParentNotClient
			case "read_failure":
				expected = errors.New("reader failed")
			case "no_reader":
				expected = ErrUnavailable
			case "ambiguous":
				parent = id
				expected = ErrInvalidArgument
			case "empty_choice":
				input = &projectClientInput{}
				expected = ErrInvalidArgument
			case "empty_name":
				name := ""
				input = &projectClientInput{NewName: &name}
				expected = ErrInvalidArgument
			case "both_choices":
				name := "Client"
				input.NewName = &name
				expected = ErrInvalidArgument
			}
			if scenario == "zero_id" {
				id = 0
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				service.entities.GetOn = func(_ context.Context, actual *gorm.DB, tenant, entity guid.ID) (core.Entity, error) {
					calls++
					require.Same(t, tx, actual)
					require.Equal(t, guid.ID(1), tenant)
					require.Equal(t, id, entity)
					client := core.Entity{ID: id, TenantID: 1, Type: "client", Status: core.EntityActive}
					switch scenario {
					case "missing":
						return core.Entity{}, core.ErrEntityNotFound
					case "read_failure":
						return core.Entity{}, expected
					case "disabled":
						client.Status = core.EntityDisabled
					case "wrong_type":
						client.Type = "project"
					case "wrong_tenant":
						client.TenantID = 2
					case "wrong_id":
						client.ID = 52
					}
					return client, nil
				}
				if scenario == "no_reader" {
					service.entities.GetOn = nil
				}
				got, err := service.clientOn(t.Context(), tx, 1, parent, input)
				if expected == nil {
					require.NoError(t, err)
					if scenario == "none" {
						require.Zero(t, got)
					} else {
						require.Equal(t, id, got)
					}
				} else {
					require.ErrorIs(t, err, expected)
					require.Zero(t, got)
				}
				return err
			})
			if expected == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, expected)
			}
			if scenario == "selected" || scenario == "internal_parent" {
				require.Equal(t, 1, calls)
			}
		})
	}
}
