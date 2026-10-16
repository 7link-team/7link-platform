package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testMembers struct{}

func (testMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) { return true, nil }
func (testMembers) IsMemberActiveOn(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (bool, error) {
	return tenant == 1 && id != 9, nil
}
func channelHarness(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "channels.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("c_")), Config{})
	m.ids, err = guid.New(1)
	require.NoError(t, err)
	m.members = testMembers{}
	m.agents = fleet.AgentDirectory{ByPrincipalOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}}
	m.principals = identity.PrincipalStateStore{
		LockActiveRow: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{ID: id, LoginName: id.String()}, id != 8, nil
		},
		HumanByLogin: func(_ context.Context, _ *gorm.DB, login string) (identity.Principal, bool, error) {
			if login == "7" {
				return identity.Principal{}, false, nil
			}
			return identity.Principal{ID: 2}, true, nil
		},
	}
	db := router.Write()
	require.NoError(t, db.Exec("CREATE TABLE audit_probe (event_type TEXT NOT NULL)").Error)
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.Contains(t, event.Detail, "reason")
		require.IsType(t, []string{}, event.Detail["evidence_refs"])
		require.NotEmpty(t, event.Detail["authority_source"])
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", event.EventType).Error
	}
	raw, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "c_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	return m, db
}
func TestDefaultChannelTransactionLifecycle(t *testing.T) {
	m, db := channelHarness(t)
	input := DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: strings.Repeat("项", 45), MemberPrincipalIDs: []guid.ID{2, 2}}
	abort := errors.New("later project step failed")
	for _, rollback := range []bool{true, false} {
		err := db.Transaction(func(tx *gorm.DB) error {
			_, err := m.CreateDefaultOn(t.Context(), tx, input)
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
		var count int64
		for _, table := range []string{"c_collab_channels", "c_collab_channel_members"} {
			require.NoError(t, db.Table(table).Count(&count).Error)
			if rollback {
				require.Zero(t, count)
			} else {
				require.EqualValues(t, 1, count)
			}
		}
	}
	var row model.Channel
	require.NoError(t, db.Table("c_collab_channels").Take(&row).Error)
	require.Len(t, []rune(row.Name), 40)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.CreateDefaultOn(t.Context(), tx, input); return err }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.RenameDefaultOn(t.Context(), tx, 2, 3, "foreign") }), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ArchiveProjectChannelsOn(t.Context(), tx, 2, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.RenameDefaultOn(t.Context(), tx, 1, 3, "Changed") }))
	require.NoError(t, db.Table("c_collab_channels").Take(&row).Error)
	require.Equal(t, "Changed", row.Name)
	require.Equal(t, 2, row.Version)
	require.Nil(t, row.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
	}))
	require.NoError(t, db.Table("c_collab_channels").Take(&row).Error)
	require.NotNil(t, row.ArchivedAt)
	require.Equal(t, 3, row.Version)
	var audits int64
	require.NoError(t, db.Table("audit_probe").Count(&audits).Error)
	require.EqualValues(t, 3, audits, "create, member added, archive; rollback and repeated archive produce none")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.RenameDefaultOn(t.Context(), tx, 1, 3, "Closed") }), ErrNotFound)
}
func TestDefaultChannelRejectsInvalidMembersAndTransactions(t *testing.T) {
	m, db := channelHarness(t)
	in := DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{2}}
	_, err := m.CreateDefaultOn(t.Context(), db, in)
	require.ErrorIs(t, err, ErrInvalidArgument)
	for _, id := range []guid.ID{0, 7, 8, 9} {
		in.MemberPrincipalIDs = []guid.ID{id}
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.CreateDefaultOn(t.Context(), tx, in); return err }))
	}
	var count int64
	require.NoError(t, db.Table("c_collab_channels").Count(&count).Error)
	require.Zero(t, count)
}

func TestDefaultChannelFailureRollsBackAllRows(t *testing.T) {
	m, db := channelHarness(t)
	require.NoError(t, db.Exec("DROP TABLE c_collab_channel_members").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{2}})
		return err
	}))
	var count int64
	require.NoError(t, db.Table("c_collab_channels").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DROP TABLE c_collab_channels").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.RenameDefaultOn(t.Context(), tx, 1, 3, "Changed") }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
	}))
}
func TestChannelProvisioningRejectsInvalidScopeAndName(t *testing.T) {
	m, db := channelHarness(t)
	for _, tx := range []*gorm.DB{nil, db} {
		_, err := m.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project"})
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.ErrorIs(t, m.RenameDefaultOn(t.Context(), tx, 1, 3, "Project"), ErrInvalidArgument)
		require.ErrorIs(t, m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}), ErrInvalidArgument)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "  "})
		return err
	}), ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.RenameDefaultOn(t.Context(), tx, 1, 3, "  ") }), ErrInvalidArgument)
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project"})
		return err
	}), ErrUnavailable)
}

func TestChannelAuditFailureRollsBackBusinessWrites(t *testing.T) {
	for _, operation := range []string{"collab.channel_created", "collab.member_added", "collab.channel_archived"} {
		t.Run(operation, func(t *testing.T) {
			m, db := channelHarness(t)
			input := DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{2}}
			create := func(tx *gorm.DB) error { _, err := m.CreateDefaultOn(t.Context(), tx, input); return err }
			if operation == "collab.channel_archived" {
				require.NoError(t, db.Transaction(create))
			}
			original := m.audit
			rejected := errors.New("audit unavailable")
			m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				if event.EventType == operation {
					return rejected
				}
				return original(ctx, tx, event)
			}
			if operation == "collab.channel_archived" {
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					return m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"})
				}), rejected)
				var row model.Channel
				require.NoError(t, db.Table("c_collab_channels").Take(&row).Error)
				require.Nil(t, row.ArchivedAt)
				require.Equal(t, 1, row.Version)
			} else {
				require.ErrorIs(t, db.Transaction(create), rejected)
				for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "audit_probe"} {
					var count int64
					require.NoError(t, db.Table(table).Count(&count).Error)
					require.Zero(t, count)
				}
			}
		})
	}
}

func TestChannelProvisioningRequiresAuditAuthority(t *testing.T) {
	m, db := channelHarness(t)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.CreateDefaultOn(t.Context(), tx, DefaultChannelInput{TenantID: 1, ProjectID: 3, Name: "Project"})
		return err
	}), ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ArchiveProjectChannelsOn(t.Context(), tx, 1, 3, ProvisioningAudit{}) }), ErrInvalidArgument)
	var count int64
	require.NoError(t, db.Table("c_collab_channels").Count(&count).Error)
	require.Zero(t, count)
}

func TestDefaultChannelClassifiesRealAgentsAndRollsBackMixedMembers(t *testing.T) {
	for _, failure := range []string{"", "unregistered", "foreign_tenant", "directory_error", "member_audit"} {
		t.Run(failure, func(t *testing.T) {
			m, db := channelHarness(t)
			fm := fleet.NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("c_")), fleet.Config{})
			source := fm.MigrationSource()
			source.Prefix = "c_"
			raw, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
			tenant := 1
			if failure == "foreign_tenant" {
				tenant = 2
			}
			if failure != "unregistered" {
				require.NoError(t, db.Table("c_fleet_agents").Create(map[string]any{"id": 70, "tenant_id": tenant, "principal_id": 7, "name": "Agent", "handle": "agent-one", "avatar_seed": 1, "system_prompt": "Private instructions", "runtime_kind": "CODEX", "model_id": "model", "model_family": "family", "created_by_principal_id": 2, "owner_principal_id": 2, "created_at": time.Now(), "updated_at": time.Now()}).Error)
			}
			originalAudit := m.audit
			injected := errors.New("injected downstream failure")
			if failure == "member_audit" {
				m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if err := originalAudit(ctx, tx, event); err != nil {
						return err
					}
					if event.EventType == "collab.member_added" && event.Detail["member_principal_id"] == "7" {
						return injected
					}
					return nil
				}
			}
			var locks []guid.ID
			lock := m.principals.LockActiveRow
			m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
				locks = append(locks, id)
				return lock(ctx, tx, id)
			}
			input := DefaultChannelInput{Audit: ProvisioningAudit{AuthoritySource: "ROLE:delivery_lead"}, TenantID: 1, ProjectID: 3, Name: "Project", MemberPrincipalIDs: []guid.ID{7, 2, 7}}
			err = db.Transaction(func(tx *gorm.DB) error {
				m.agents = fleet.AgentDirectory{ByPrincipalOn: func(ctx context.Context, got *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
					require.Same(t, tx, got)
					if failure == "directory_error" {
						return fleet.AgentIdentity{}, false, injected
					}
					return fm.ByPrincipalOn(ctx, got, tenant, principal)
				}}
				_, e := m.CreateDefaultOn(t.Context(), tx, input)
				return e
			})
			require.Equal(t, []guid.ID{2, 7}, locks, "deduplicated stable lock order")
			require.Equal(t, []guid.ID{7, 2, 7}, input.MemberPrincipalIDs, "caller input must not be sorted in place")
			if failure != "" {
				if failure == "directory_error" || failure == "member_audit" {
					require.ErrorIs(t, err, injected)
				} else {
					require.ErrorIs(t, err, ErrMemberInactive)
				}
				for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "audit_probe"} {
					var count int64
					require.NoError(t, db.Table(table).Count(&count).Error)
					require.Zero(t, count, table)
				}
				return
			}
			require.NoError(t, err)
			var members []model.ChannelMember
			require.NoError(t, db.Table("c_collab_channel_members").Order("principal_id").Find(&members).Error)
			require.Len(t, members, 2)
			require.Equal(t, "HUMAN", members[0].MemberKind)
			require.Equal(t, "AGENT", members[1].MemberKind)
			require.Equal(t, guid.ID(7), members[1].PrincipalID)
			require.Equal(t, "DERIVED", members[1].Source)
		})
	}
}
