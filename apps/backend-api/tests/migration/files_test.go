package migration_test

import (
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFileMigrationsOperationCostAndPermissionsAcrossEngines(t *testing.T) {
	for _, engine := range []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	} {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					sqlDB := engine.open(t)
					source := (&files.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, engine.dialect, 8))
					var dialector gorm.Dialector
					switch engine.dialect {
					case "mysql":
						dialector = mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true})
					case "postgres":
						dialector = postgres.New(postgres.Config{Conn: sqlDB})
					default:
						dialector = sqlite.New(sqlite.Config{Conn: sqlDB})
					}
					db, err := gorm.Open(dialector, &gorm.Config{})
					require.NoError(t, err)
					q := query.Bind(db, prefix)
					validUpload := model.Upload{
						ID: 1000, PrincipalID: 90, BackendID: "local", StorageKey: "upload-1000", StorageHandle: "upload-1000",
						Name: "upload.bin", Size: 10, Offset: 10, ChecksumState: "pending", State: "open", ReleaseState: "pending",
						LastProgressAt: 1, ExpiresAt: 2, Version: 1, CreatedAt: 1,
					}
					require.NoError(t, q.Upload.WithContext(t.Context()).Create(&validUpload))
					for id, offset := range map[guid.ID]int64{1001: -1, 1002: 11} {
						invalidUpload := validUpload
						invalidUpload.ID, invalidUpload.StorageKey, invalidUpload.StorageHandle, invalidUpload.Offset = id, fmt.Sprintf("upload-%d", id), fmt.Sprintf("upload-%d", id), offset
						require.Error(t, q.Upload.WithContext(t.Context()).Create(&invalidUpload), "committed offset %d must stay within the upload size", offset)
					}
					owner, slot := guid.ID(90), 1
					require.NoError(t, q.Space.WithContext(t.Context()).Omit(q.Space.SystemSlot).Create(&model.Space{ID: 1, ScopeKind: "account", PrincipalID: &owner, Version: 1, CreatedAt: 1}))
					require.Error(t, q.TrashBatch.WithContext(t.Context()).Create(&model.TrashBatch{
						ID: 1003, SpaceID: 1, DeletedBy: owner, DeletedAt: 1, State: "active", NodeCount: 1, RootCount: 2,
					}), "trash roots cannot exceed the batch node count")
					require.NoError(t, q.Node.WithContext(t.Context()).Omit(q.Node.RootScope, q.Node.RootOwnerID, q.Node.ACLStopInheritance, q.Node.ManagementMode).Create(&model.Node{ID: 2, SpaceID: 1, ParentKind: "directory", RootSlot: &slot, LiveSlot: &slot, Kind: "directory", State: "active", Version: 1, CreatedAt: 1, UpdatedAt: 1}))
					require.NoError(t, q.NodeClosure.WithContext(t.Context()).Create(&model.NodeClosure{SpaceID: 1, AncestorID: 2, DescendantID: 2, Depth: 0}))
					require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, engine.dialect))
					root, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).Take()
					require.NoError(t, err)
					require.Nil(t, root.RootScope, "existing nodes have no implicit root ownership or grant")
					require.Equal(t, "user", root.ManagementMode, "existing logical files remain user-managed")
					_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(q.Node.RootScope.Value("account"), q.Node.RootOwnerID.Value(owner), q.Node.ACLStopInheritance.Value(true))
					require.NoError(t, err)
					scope, parent := "account", guid.ID(2)
					require.Error(t, q.Node.WithContext(t.Context()).Create(&model.Node{
						ID: 3, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot,
						Kind: "directory", Name: "duplicate", NameKey: "duplicate", State: "active", Version: 1,
						RootScope: &scope, RootOwnerID: &owner, CreatedAt: 1, UpdatedAt: 1,
					}), "one space cannot contain duplicate scope/owner roots")
					acl := model.NodeACL{ID: 200, SpaceID: 1, NodeID: 2, SubjectKind: "principal", SubjectID: 90, Effect: "allow", Action: "read", Applies: "self_and_descendants", CreatedBy: 90, CreatedAt: 1}
					require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&acl))
					storedACL, err := q.NodeACL.WithContext(t.Context()).Where(q.NodeACL.ID.Eq(acl.ID)).Take()
					require.NoError(t, err)
					require.Equal(t, acl, *storedACL)
					root, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).Take()
					require.NoError(t, err)
					require.True(t, root.ACLStopInheritance)
					file := model.Node{
						ID: 4, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot,
						Kind: "file", Name: "avatar.png", NameKey: "avatar.png", Ext: "png", State: "active",
						Version: 1, ManagementMode: "resource", CreatedAt: 1, UpdatedAt: 1,
					}
					require.NoError(t, q.Node.WithContext(t.Context()).Create(&file))
					binding := model.ResourceBinding{
						ID: 300, SpaceID: 1, FileID: file.ID, FileKind: "file", Purpose: "principal.avatar",
						ResourceKind: "principal", ResourceID: owner, Slot: "current", State: "active", LiveSlot: &slot,
						Version: 1, CreatedBy: owner, CreatedAt: 1, UpdatedAt: 1,
					}
					require.NoError(t, q.ResourceBinding.WithContext(t.Context()).Create(&binding))
					storedBinding, err := q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.ID.Eq(binding.ID)).Take()
					require.NoError(t, err)
					require.Equal(t, binding.FileID, storedBinding.FileID)
					for _, failure := range []string{"duplicate", "foreign-space", "invalid-action", "invalid-effect"} {
						invalid := acl
						invalid.ID = 201
						switch failure {
						case "foreign-space":
							invalid.SpaceID = 999
						case "invalid-action":
							invalid.Action = "*"
						case "invalid-effect":
							invalid.Effect = "grant"
						}
						require.Error(t, q.NodeACL.WithContext(t.Context()).Create(&invalid), failure)
					}
					plan := `{"nodes":1,"closure_rows":3,"created":1,"moved":0,"overwritten":0,"skipped":0,"recycled":0}`
					op := model.Operation{ID: 100, SpaceID: 1, ActorID: 90, IdempotencyKey: "cost-migration", RequestHash: strings.Repeat("0", 64), RequestJSON: "{}", RootsJSON: "[]", Action: "copy", State: "queued", Revision: 1, SpaceRevision: 1, DestinationID: 2, DestinationVersion: 1, NodeBudget: 10, ClosureBudget: 100, PlanJSON: plan, ResultJSON: "{}", CreatedAt: 1, ExpiresAt: 60000}
					require.NoError(t, q.Operation.WithContext(t.Context()).Create(&op))
					read, err := q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Take()
					require.NoError(t, err)
					require.Equal(t, plan, read.PlanJSON)
					require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, engine.dialect, 7))
					require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, engine.dialect))
					read, err = q.Operation.WithContext(t.Context()).Where(q.Operation.ID.Eq(op.ID)).Take()
					require.NoError(t, err)
					require.Equal(t, "{}", read.PlanJSON, "legacy plans need replanning, not fabricated estimates")
					require.Equal(t, op.IdempotencyKey, read.IdempotencyKey)
					root, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).Take()
					require.NoError(t, err)
					require.Nil(t, root.RootScope)
					require.False(t, root.ACLStopInheritance)
					bindingCount, err := q.ResourceBinding.WithContext(t.Context()).Count()
					require.NoError(t, err)
					require.Zero(t, bindingCount, "rolling below the binding migration removes only the retired ledger")
					aclCount, err := q.NodeACL.WithContext(t.Context()).Count()
					require.NoError(t, err)
					require.Zero(t, aclCount, "down/up never invents permissions for old nodes")
					require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, engine.dialect, 0))
					require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, engine.dialect))
					count, err := q.Operation.WithContext(t.Context()).Count()
					require.NoError(t, err)
					require.Zero(t, count)
				})
			}
		})
	}
}
