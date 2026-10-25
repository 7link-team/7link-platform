package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type channelMembers struct{}

func TestCollabRootContextMigrationPreservesExistingMessageOrder(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			db := openMilestoneGORM(t, raw, engine.dialect)
			m := collab.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1")), collab.Config{})
			source := m.MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.MigrateUpTo(t.Context(), raw, source, engine.dialect, 7))
			now := time.Now().UTC().Truncate(time.Millisecond)
			for _, id := range []int64{10, 20} {
				require.NoError(t, db.Table(deploymentPrefix+"collab_messages").Create(map[string]any{"id": id, "tenant_id": 1, "channel_id": 3, "seq": id, "author_id": 2, "body": "Existing context", "visibility": "CHANNEL", "version": 1, "created_at": now, "updated_at": now}).Error)
			}
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			var rows []model.Message
			require.NoError(t, db.Table(deploymentPrefix+"collab_messages").Order("seq").Find(&rows).Error)
			require.Len(t, rows, 2)
			for i, row := range rows {
				require.EqualValues(t, (i+1)*10, row.Seq)
				require.Equal(t, row.Seq, row.ContextSeq)
				require.Equal(t, "Existing context", row.Body)
			}
		})
	}
}

func (channelMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (channelMembers) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func TestCollabSchemasAndTransactionsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	engines = append(engines, realMySQLEngines(t)...)
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			raw := engine.open(t)
			db := openMilestoneGORM(t, raw, engine.dialect)
			rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix(deploymentPrefix))
			m := collab.NewModule(rt, collab.Config{})
			source := m.MigrationSource()
			source.Prefix = deploymentPrefix
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, engine.dialect))
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Channel{}, deploymentPrefix+"collab_channels")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ChannelMember{}, deploymentPrefix+"collab_channel_members")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.Message{}, deploymentPrefix+"collab_messages")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.MessageRevision{}, deploymentPrefix+"collab_message_revisions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.MessageMention{}, deploymentPrefix+"collab_message_mentions")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SavedMessage{}, deploymentPrefix+"collab_saved_messages")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.MessageAttachment{}, deploymentPrefix+"collab_message_attachments")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.MessageWork{}, deploymentPrefix+"collab_message_work_items")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ChannelConnector{}, deploymentPrefix+"collab_channel_connectors")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.ChannelDelivery{}, deploymentPrefix+"collab_channel_deliveries")
			assertModelMatchesNamedTable(t, raw, engine.dialect, model.SystemCard{}, deploymentPrefix+"collab_system_cards")
			if engine.name == "mysql" {
				return
			} // The in-memory harness is not real MySQL transaction validation.
			ids, err := guid.New(1)
			require.NoError(t, err)
			services := module.NewServices()
			require.NoError(t, module.Provide[files.ConversationResources](services, files.ConversationResources{}))
			require.NoError(t, module.Provide[fleet.AgentDirectory](services, fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{}, false, nil
			}}))
			require.NoError(t, module.Provide[guid.Source](services, ids))
			require.NoError(t, module.Provide[core.ActiveMemberChecker](services, channelMembers{}))
			require.NoError(t, module.Provide[identity.PrincipalStateStore](services, identity.PrincipalStateStore{
				LockActiveRow: func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{ID: 2, LoginName: "human"}, true, nil
				},
				HumanByLogin: func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
					return identity.Principal{ID: 2}, true, nil
				},
			}))
			require.NoError(t, module.Provide[audit.Appender](services, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil }))
			require.NoError(t, m.ProvideServices(services))
			require.NoError(t, module.Provide[core.EntityConstraintOn](services, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			})))
			require.NoError(t, module.Provide[core.TenantConstraintOn](services, core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			})))
			require.NoError(t, module.Provide[core.EntityOwnerResolver](services, func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
				return authz.ResourceOwner{}, false, nil
			}))
			registry, registryErr := authz.NewRegistry()
			require.NoError(t, registryErr)
			require.NoError(t, module.Provide[*authz.Registrar](services, authz.NewDeclarationOnlyRegistrar(registry)))
			publisher := events.NewModule(rt)
			require.NoError(t, publisher.ProvideServices(services))
			require.NoError(t, idempotency.NewModule(rt).ProvideServices(services))
			bindings, bindingErr := files.NewResourceBindings(deploymentPrefix)
			require.NoError(t, bindingErr)
			require.NoError(t, module.Provide[*files.ResourceBindings](services, bindings))
			require.NoError(t, services.Seal())
			require.NoError(t, m.InjectServices(services))
			require.NoError(t, m.Initialize(t.Context()))
			p, err := module.Resolve[collab.ChannelProvisioner](services)
			require.NoError(t, err)
			abort := errors.New("project transaction rolled back")
			for _, rollback := range []bool{true, false} {
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := p.CreateDefaultOn(t.Context(), tx, collab.DefaultChannelInput{Audit: collab.ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{2, 2}})
					if err != nil {
						return err
					}
					if rollback {
						return abort
					}
					return nil
				})
				if rollback {
					require.ErrorIs(t, err, abort)
				} else {
					require.NoError(t, err)
				}
				for _, table := range []string{"collab_channels", "collab_channel_members"} {
					var count int64
					require.NoError(t, db.Table(deploymentPrefix+table).Count(&count).Error)
					if rollback {
						require.Zero(t, count)
					} else {
						require.EqualValues(t, 1, count)
					}
				}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return p.RenameDefaultOn(t.Context(), tx, 1, 3, "Renamed") }))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return p.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, collab.ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
			}))
			var row model.Channel
			require.NoError(t, db.Table(deploymentPrefix+"collab_channels").Take(&row).Error)
			require.Equal(t, "Renamed", row.Name)
			require.NotNil(t, row.ArchivedAt)
			require.Equal(t, 3, row.Version)
		})
	}
}
