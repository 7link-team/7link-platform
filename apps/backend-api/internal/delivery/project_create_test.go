package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCreateProjectCommandAuthorizationAndAtomicity(t *testing.T) {
	for _, scenario := range []string{"idempotent_success", "idempotent_error", "idempotent_abort", "idempotent_retry", "success", "denied", "nonhuman", "changed_human", "empty_source", "seed", "lock", "authority", "reference", "member", "event", "limit", "last_slot", "archived", "other_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			service, db, base := projectHarness(t)
			api, body := projectContractAPI(t)
			response := api.Post("/projects", strings.NewReader(`{"name":"Project","code":"AB","members":[{"principal_id":"12","role_key":"engineer"},{"principal_id":"10","role_key":"engineer"}]}`))
			require.Equal(t, http.StatusOK, response.Code)
			injected := errors.New("injected dependency failure")
			var sequence []string
			var commandTx *gorm.DB
			write := func(tx *gorm.DB, step string) error {
				require.Same(t, commandTx, tx)
				sequence = append(sequence, step)
				return tx.Exec("INSERT INTO provision_probe VALUES (?)", step).Error
			}
			m := &Module{service: service, ids: draftIDSource(func() (int64, error) { return 90, nil })}
			m.principals = identity.PrincipalStateStore{
				ActiveHumanByID: func(_ context.Context, handle *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if handle == db {
						sequence = append(sequence, "classify")
					} else {
						require.Same(t, commandTx, handle)
					}
					return identity.Principal{ID: id}, scenario != "nonhuman" && !(scenario == "changed_human" && handle != db), nil
				},
				Lock: func(_ context.Context, tx *gorm.DB, id guid.ID) error {
					if commandTx == nil {
						commandTx = tx
					}
					require.Same(t, commandTx, tx)
					sequence = append(sequence, "lock:"+id.String())
					if scenario == "lock" {
						return injected
					}
					return nil
				},
			}
			m.roles = governance.SeededRoles{
				EnsureTenant: func(_ context.Context, tenant guid.ID) error {
					require.Nil(t, commandTx)
					require.Equal(t, guid.ID(1), tenant)
					sequence = append(sequence, "seed")
					if scenario == "seed" {
						return injected
					}
					return nil
				},
				RoleIDOn: func(_ context.Context, tx *gorm.DB, tenant guid.ID, key string) (guid.ID, error) {
					require.Same(t, commandTx, tx)
					return 6, nil
				},
			}
			m.createAuthority = func(_ context.Context, tx *gorm.DB, tenant, principal guid.ID, kind string) (governance.AuthorityDecision, error) {
				require.Same(t, commandTx, tx)
				require.Equal(t, guid.ID(11), principal)
				require.Equal(t, "HUMAN", kind)
				require.Equal(t, []string{"classify", "seed", "lock:10", "lock:11", "lock:12"}, sequence)
				sequence = append(sequence, "authorize")
				if scenario == "authority" {
					return governance.AuthorityDecision{}, injected
				}
				source := "ROLE:team_owner"
				if scenario == "empty_source" {
					source = ""
				}
				return governance.AuthorityDecision{Allowed: scenario != "denied", AuthoritySource: source}, nil
			}
			m.sources = intake.IntakeReader{
				GetOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (intake.IntakeRef, bool, error) {
					return intake.IntakeRef{}, false, injected
				},
				ContactsOn: func(context.Context, *gorm.DB, guid.ID, []guid.ID) ([]intake.ContactReference, error) {
					return nil, nil
				},
			}
			if scenario == "reference" {
				id := guid.ID(23)
				body.OriginIntakeID = &id
			}
			m.members = memberCheck(func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) (bool, error) {
				require.Same(t, commandTx, tx)
				return scenario != "member", nil
			})
			service.entities.GetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (core.Entity, error) {
				return core.Entity{}, nil
			}
			service.entities.CreateOn = func(_ context.Context, tx *gorm.DB, e core.Entity) (core.Entity, error) {
				e.ID = 99
				return e, write(tx, "entity")
			}
			service.roles.PutOn = func(_ context.Context, tx *gorm.DB, tenant, entity, role, principal guid.ID, effect core.BindingEffect, expiry time.Time) (core.EntityRoleBinding, bool, error) {
				return core.EntityRoleBinding{}, true, write(tx, "binding")
			}
			service.channels.CreateDefaultOn = func(_ context.Context, tx *gorm.DB, in collab.DefaultChannelInput) (guid.ID, error) {
				require.Equal(t, "ROLE:team_owner", in.Audit.AuthoritySource)
				return 8, write(tx, "channel")
			}
			service.audit = func(_ context.Context, tx *gorm.DB, e audit.DomainEvent) error { return write(tx, "audit") }
			service.events = projectPublisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
				require.Equal(t, "ROLE:team_owner", e.Actor.AuthoritySource)
				err := write(tx, "event")
				if scenario == "idempotent_error" {
					return 0, ErrProjectLimit
				}
				if scenario == "event" || scenario == "idempotent_retry" {
					return 0, injected
				}
				return 9, err
			})
			var before int64
			if scenario == "limit" || scenario == "last_slot" || scenario == "archived" || scenario == "other_tenant" {
				size := 500
				if scenario == "last_slot" {
					size = 499
				}
				rows := make([]model.Project, size)
				for i := range rows {
					rows[i] = base.project
					rows[i].ProjectID = guid.ID(1000 + i)
					rows[i].Code = fmt.Sprintf("P%d", i)
					if scenario == "archived" {
						now := time.Now()
						rows[i].ArchivedAt = &now
						rows[i].Stage = "CLOSED"
						rows[i].ClosedAt = &now
					}
					if scenario == "other_tenant" {
						rows[i].TenantID = 2
					}
				}
				require.NoError(t, db.Table(service.names.Projects.String()).CreateInBatches(rows, 100).Error)
				before = int64(size)
			}
			ctx := authn.WithClaims(t.Context(), &authn.Claims{SubjectType: authn.SubjectTypePrincipal, TenantID: 1, PrincipalID: 11})
			if strings.HasPrefix(scenario, "idempotent_") {
				store := idempotency.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("d_")))
				raw, err := db.DB()
				require.NoError(t, err)
				source := store.MigrationSource()
				source.Prefix = "d_"
				require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
				services := module.NewServices()
				ids, err := guid.New(1)
				require.NoError(t, err)
				require.NoError(t, module.Provide[guid.Source](services, ids))
				require.NoError(t, services.Seal())
				require.NoError(t, store.InjectServices(services))
				require.NoError(t, platformi18n.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), platformi18n.Base).Initialize(ctx))
				require.NoError(t, store.Initialize(ctx))
				hash, err := idempotency.HashRequest("POST", "/tenants/{tenant_id}/projects", map[string]string{"tenant_id": "1"}, *body)
				require.NoError(t, err)
				key := idempotency.Key{TenantID: 1, SubjectKind: "PRINCIPAL", SubjectID: 11, Operation: "project.create", Value: "create-once", RequestHash: hash}
				var first idempotency.Result
				for attempt := 0; attempt < 2; attempt++ {
					sequence = nil
					commandTx = nil
					principalIDs, err := m.prepareProjectCreation(ctx, db, 1, 11, *body)
					require.NoError(t, err)
					var response idempotency.Result
					var replayed bool
					err = db.Transaction(func(tx *gorm.DB) error {
						var err error
						response, replayed, err = store.Do(ctx, tx, key, func(work *gorm.DB) (idempotency.Result, error) {
							created, err := m.createProjectOn(ctx, work, 1, 11, *body, collab.ProvisioningAudit{}, principalIDs)
							if errors.Is(err, ErrProjectLimit) {
								return idempotency.Result{StatusCode: 422, Body: json.RawMessage(`{"code":127044}`)}, nil
							}
							if err != nil {
								return idempotency.Result{}, err
							}
							encoded, err := json.Marshal(struct {
								ProjectID guid.ID `json:"project_id"`
								ChannelID guid.ID `json:"channel_id"`
							}{created.project.ProjectID, created.channelID})
							return idempotency.Result{StatusCode: 201, Body: encoded, ResourceKind: "project", ResourceID: created.project.ProjectID}, err
						})
						if err != nil {
							return err
						}
						if scenario == "idempotent_abort" {
							return injected
						}
						return nil
					})
					if scenario == "idempotent_abort" || scenario == "idempotent_retry" {
						require.ErrorIs(t, err, injected)
						require.False(t, replayed)
					} else {
						require.NoError(t, err)
						require.Equal(t, attempt == 1, replayed)
						if attempt == 0 {
							first = response
						} else {
							require.Equal(t, first, response)
							require.Equal(t, []string{"classify", "seed"}, sequence, "replay must not execute authorization/bindings/channel/outbox again")
						}
					}
				}
				var projects, probes, keys int64
				require.NoError(t, db.Table(service.names.Projects.String()).Count(&projects).Error)
				require.NoError(t, db.Table("provision_probe").Count(&probes).Error)
				require.NoError(t, db.Table("d_platform_idempotency_keys").Count(&keys).Error)
				switch scenario {
				case "idempotent_success":
					require.EqualValues(t, 1, projects)
					require.EqualValues(t, 7, probes)
					require.EqualValues(t, 1, keys)
				case "idempotent_error":
					require.Zero(t, projects)
					require.Zero(t, probes)
					require.EqualValues(t, 1, keys)
					require.Equal(t, 422, first.StatusCode)
				default:
					require.Zero(t, projects)
					require.Zero(t, probes)
					require.Zero(t, keys)
				}
				return
			}
			result, err := m.createProject(ctx, db, 1, 11, *body, collab.ProvisioningAudit{AuthoritySource: "untrusted"})
			success := scenario == "success" || scenario == "last_slot" || scenario == "archived" || scenario == "other_tenant"
			var projects, probes int64
			require.NoError(t, db.Table(service.names.Projects.String()).Count(&projects).Error)
			require.NoError(t, db.Table("provision_probe").Count(&probes).Error)
			if success {
				require.NoError(t, err)
				require.Equal(t, guid.ID(99), result.project.ProjectID)
				require.Equal(t, before+1, projects)
				require.EqualValues(t, 7, probes)
			} else {
				require.Error(t, err)
				require.Equal(t, projectCreated{}, result)
				require.Equal(t, before, projects)
				require.Zero(t, probes)
				switch scenario {
				case "limit":
					require.ErrorIs(t, err, ErrProjectLimit)
				case "denied":
					require.ErrorIs(t, err, ErrProjectForbidden)
				case "nonhuman", "changed_human":
					require.ErrorIs(t, err, ErrProjectNonDelegable)
				case "empty_source":
					require.ErrorIs(t, err, ErrUnavailable)
				case "member":
					require.ErrorIs(t, err, ErrInvalidMember)
				default:
					require.ErrorIs(t, err, injected)
				}
			}
		})
	}
}

func TestCreateProjectRejectsInvalidCommandBoundary(t *testing.T) {
	_, db, _ := projectHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11})
	m := &Module{}
	for _, candidate := range []*gorm.DB{nil, {}} {
		_, err := m.createProject(ctx, candidate, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{})
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.createProjectOn(ctx, candidate, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{}, nil)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, boundaryErr := m.createProjectOn(ctx, db, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{}, nil)
	require.ErrorIs(t, boundaryErr, ErrInvalidArgument, "caller-owned write must reject a nontransactional connection")
	for _, ids := range [][2]guid.ID{{0, 11}, {1, 0}} {
		_, err := m.createProject(ctx, db, ids[0], ids[1], projectCreateBody{}, collab.ProvisioningAudit{})
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err := m.createProject(ctx, db, 1, 11, projectCreateBody{Members: make([]projectMemberInput, 51)}, collab.ProvisioningAudit{})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createProject(ctx, tx, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{})
		require.ErrorIs(t, err, ErrInvalidArgument)
		return nil
	}))
	_, err = m.createProject(t.Context(), db, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{})
	require.ErrorIs(t, err, ErrProjectForbidden)
	_, err = m.createProject(ctx, db, 1, 12, projectCreateBody{}, collab.ProvisioningAudit{})
	require.ErrorIs(t, err, ErrProjectForbidden)
	for _, candidate := range []*Module{nil, m} {
		_, err = candidate.createProject(ctx, db, 1, 11, projectCreateBody{}, collab.ProvisioningAudit{})
		require.ErrorIs(t, err, ErrUnavailable)
	}
}
