package app

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/fleet"
	businessgovernance "7link.com/platform/internal/governance"
	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/iam/webhook"
	infraaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/notification"
	"7link.com/platform/internal/performance"
	"7link.com/platform/internal/planning"
	"7link.com/platform/internal/templates"
	"7link.com/platform/internal/todo"
	"7link.com/platform/internal/yunxiao"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRunMigrationsUpStatusValidateAndDown(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "migrations.sqlite")
	config := migrationTestConfig(map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      databasePath,
			Writable: true,
		},
	})
	config.Action = MigrationUp

	reports, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)
	assert.Equal(t, []string{"dlock", "wuid", "events", "idempotency", "audit", "jobs"}, migrationReportNames(reports))
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	// The standalone command stops after migration. In particular, wuid must not
	// enter Start and allocate a worker lease.
	db, err := gorm.Open(sqlite.Open(databasePath))
	require.NoError(t, err)
	var workerCount int64
	require.NoError(t, db.Table("worker_ids").Count(&workerCount).Error)
	assert.Zero(t, workerCount)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
	assert.Equal(t, goosex.StateClean, reports[1].Status.State)

	config.Action = MigrationStatus
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)

	config.Action = MigrationValidate
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 6)

	config.Action = MigrationDown
	config.Module = "wuid"
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationValidate
	config.Module = ""
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "wuid migration state is pending")

	config.Action = MigrationUpTo
	config.Module = "wuid"
	config.To = 1
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)

	config.Action = MigrationDownTo
	config.To = 0
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StatePending, reports[0].Status.State)

	config.Action = MigrationAuto
	reports, err = (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, goosex.StateClean, reports[0].Status.State)
}

func migrationTestConfig(database map[string]Database) MigrationConfig {
	config := validTestAppConfig()
	config.Database = database
	return MigrationConfig{AppConfig: config}
}

func migrationReportNames(reports []MigrationReport) []string {
	names := make([]string, len(reports))
	for index, report := range reports {
		names[index] = report.Module
	}
	return names
}

func TestRunMigrationsRejectsUnknownModuleAndMissingWriter(t *testing.T) {
	base := migrationTestConfig(nil)
	base.Action = MigrationStatus
	_, err := (&App{}).RunMigrations(t.Context(), base)
	assert.ErrorContains(t, err, "writable database is not configured")

	config := migrationTestConfig(map[string]Database{
		"primary": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "unknown.sqlite"), Writable: true},
	})
	config.Action = MigrationStatus
	config.Module = "missing"
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, `module "missing" is not registered`)
}

func TestRunMigrationsUsesSingleWriteHandle(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "writer-a.sqlite")
	secondPath := filepath.Join(t.TempDir(), "writer-b.sqlite")
	config := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
		"writer-b": {Type: "sqlite", Dsn: secondPath, Writable: true},
	})
	config.Action = MigrationUp

	_, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)

	first, err := gorm.Open(sqlite.Open(firstPath))
	require.NoError(t, err)
	second, err := gorm.Open(sqlite.Open(secondPath))
	require.NoError(t, err)
	for _, db := range []*gorm.DB{first, second} {
		sqlDB, sqlErr := db.DB()
		require.NoError(t, sqlErr)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	assert.True(t, first.Migrator().HasTable("dlocks"), "the first selected writer must execute the migration")
	assert.False(t, second.Migrator().HasTable("dlocks"), "one migration must not advance to another writer")
}

func TestMigrationInspectorDoesNotAdvanceTheWriteHandleBeforeInspection(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "writer-a.sqlite")
	secondPath := filepath.Join(t.TempDir(), "writer-b.sqlite")
	seed := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
	})
	seed.Action = MigrationUp
	_, err := (&App{}).RunMigrations(t.Context(), seed)
	require.NoError(t, err)

	config := migrationTestConfig(map[string]Database{
		"writer-a": {Type: "sqlite", Dsn: firstPath, Writable: true},
		"writer-b": {Type: "sqlite", Dsn: secondPath, Writable: true},
	})
	app := &App{config: config.AppConfig}
	require.NoError(t, app.prepare(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	reports, err := app.migrationInspector(t.Context())
	require.NoError(t, err)
	require.Len(t, reports, 6)
	for _, report := range reports {
		assert.Equal(t, goosex.StateClean, report.Status.State, report.Module)
	}
}

func TestMigrateModulesRequiresRuntimeAndComposedModules(t *testing.T) {
	_, err := (&App{}).migrateModules(t.Context(), MigrationRequest{Action: MigrationStatus})
	assert.EqualError(t, err, "migration: writable database is not configured")

	app := &App{config: AppConfig{Timezone: "UTC", Database: map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "composition.sqlite"),
			Writable: true,
		},
	}}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { require.NoError(t, app.runtime.Close()) })

	_, err = app.migrateModules(t.Context(), MigrationRequest{Action: MigrationStatus})
	assert.EqualError(t, err, "migration: modules are not composed")
}

func TestRunModuleMigrationsValidatesRequests(t *testing.T) {
	_, err := executeModuleMigrations(t.Context(), nil, nil, MigrationRequest{}, "")
	assert.EqualError(t, err, "migration: writable database is unavailable")

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "validation.sqlite")))
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: "sideways"}, "")
	assert.EqualError(t, err, `migration: unsupported action "sideways"`)
	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: MigrationDown}, "")
	assert.EqualError(t, err, "migration: down requires a module")
	_, err = executeModuleMigrations(t.Context(), db, nil, MigrationRequest{Action: MigrationDownTo, Module: "wuid", To: -1}, "")
	assert.EqualError(t, err, "migration: target version cannot be negative")
}

func TestRunMigrationsRejectsInvalidDownTargets(t *testing.T) {
	config := migrationTestConfig(map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "down-target.sqlite"),
			Writable: true,
		},
	})
	config.Action = MigrationUp
	config.Module = "wuid"
	reports, err := (&App{}).RunMigrations(t.Context(), config)
	require.NoError(t, err)
	require.Len(t, reports, 1)

	config.Action = MigrationDownTo
	config.To = reports[0].Status.Target + 1
	_, err = (&App{}).RunMigrations(t.Context(), config)
	assert.ErrorContains(t, err, "higher version")
}

type milestoneTestModule struct {
	name       string
	source     goosex.Source
	milestones []module.MigrationMilestone
}

func (m milestoneTestModule) ModuleName() string { return m.name }

func (m milestoneTestModule) MigrationSource() goosex.Source { return m.source }

func (m milestoneTestModule) MigrationMilestones() []module.MigrationMilestone {
	return m.milestones
}

type ordinaryMigrationTestModule struct {
	name   string
	source goosex.Source
}

func (m ordinaryMigrationTestModule) ModuleName() string { return m.name }

func (m ordinaryMigrationTestModule) MigrationSource() goosex.Source { return m.source }

func TestMigrationMilestoneGraphRejectsInvalidDeclarationsBeforeWriting(t *testing.T) {
	tests := []struct {
		name    string
		modules []module.Module
		want    string
	}{
		{
			name: "duplicate node",
			modules: []module.Module{newMilestoneTestModule("a",
				milestone("base", 1), milestone("base", 2))},
			want: `duplicate migration milestone "a.base"`,
		},
		{
			name: "missing dependency",
			modules: []module.Module{newMilestoneTestModule("a",
				milestone("base", 1, milestoneRef("missing", "base")))},
			want: `migration milestone "a.base" requires unknown milestone "missing.base"`,
		},
		{
			name: "cycle",
			modules: []module.Module{
				newMilestoneTestModule("a", milestone("base", 1, milestoneRef("b", "base"))),
				newMilestoneTestModule("b", milestone("base", 1, milestoneRef("a", "base"))),
			},
			want: "migration milestone dependency cycle",
		},
		{
			name: "source version moves backward",
			modules: []module.Module{newMilestoneTestModule("a",
				milestone("base", 2), milestone("constraints", 1))},
			want: `migration milestones for module "a" must have strictly increasing versions`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openMilestoneSQLite(t)
			_, err := ExecuteModuleMigrations(t.Context(), db, tt.modules, MigrationRequest{Action: MigrationUp}, "")
			assert.ErrorContains(t, err, tt.want)
			assert.False(t, db.Migrator().HasTable("milestone_trace"), "validation must finish before the first migration")
		})
	}
}

func TestMigrationMilestonesRunTopologicallyAndReportCurrentNode(t *testing.T) {
	db := openMilestoneSQLite(t)
	modules := milestoneTestModules()

	reports, err := ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{Action: MigrationUp}, "")
	require.NoError(t, err)
	require.Len(t, reports, 2)
	assert.Equal(t, []string{"a", "b"}, migrationReportNames(reports))
	for _, report := range reports {
		assert.Equal(t, goosex.StateClean, report.Status.State)
		assert.Equal(t, "constraints", report.Milestone)
	}

	var nodes []string
	require.NoError(t, db.Table("milestone_trace").Order("position").Pluck("node", &nodes).Error)
	assert.Equal(t, []string{"a.base", "b.base", "a.constraints", "b.constraints"}, nodes)
}

func TestMigrationMilestoneDownUsesReverseTopologyAndProtectsAppliedDependents(t *testing.T) {
	db := openMilestoneSQLite(t)
	modules := milestoneTestModules()
	_, err := ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{Action: MigrationUp}, "")
	require.NoError(t, err)

	reports, err := ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{
		Action: MigrationDown,
		Module: "a",
	}, "")
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "base", reports[0].Milestone)

	reports, err = ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{
		Action: MigrationDown,
		Module: "b",
	}, "")
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "base", reports[0].Milestone)

	_, err = ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{
		Action: MigrationDown,
		Module: "a",
	}, "")
	assert.ErrorContains(t, err, `cannot roll back migration milestone "a.base"`)
	assert.ErrorContains(t, err, `applied dependents: "b.base"`)

	_, err = ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{Action: MigrationDown, Module: "b"}, "")
	require.NoError(t, err)
	_, err = ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{Action: MigrationDown, Module: "a"}, "")
	require.NoError(t, err)
	assert.False(t, db.Migrator().HasTable("milestone_trace"))
}

func TestModuleWithoutMilestonesStillMigratesItsWholeSource(t *testing.T) {
	db := openMilestoneSQLite(t)
	ordinary := ordinaryMigrationTestModule{
		name: "ordinary",
		source: goosex.Source{TableName: "goose_ordinary", FS: fstest.MapFS{
			"sql/sqlite/00001_first.sql": {Data: []byte(`-- +goose Up
CREATE TABLE ordinary_first (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE ordinary_first;
`)},
			"sql/sqlite/00002_second.sql": {Data: []byte(`-- +goose Up
CREATE TABLE ordinary_second (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE ordinary_second;
`)},
		}},
	}

	reports, err := ExecuteModuleMigrations(t.Context(), db, []module.Module{ordinary}, MigrationRequest{Action: MigrationUp}, "")
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, int64(2), reports[0].Status.Current)
	assert.Empty(t, reports[0].Milestone)
	assert.True(t, db.Migrator().HasTable("ordinary_first"))
	assert.True(t, db.Migrator().HasTable("ordinary_second"))
}

func TestSelectedOrdinaryModuleDoesNotInspectUnselectedMilestoneSources(t *testing.T) {
	db := openMilestoneSQLite(t)
	ordinary := ordinaryMigrationTestModule{
		name: "ordinary",
		source: goosex.Source{TableName: "goose_ordinary", FS: fstest.MapFS{
			"sql/sqlite/00001_ordinary.sql": {Data: []byte(`-- +goose Up
CREATE TABLE selected_ordinary (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE selected_ordinary;
`)},
		}},
	}
	brokenUnselected := newMilestoneTestModule("broken", milestone("base", 99))

	reports, err := ExecuteModuleMigrations(t.Context(), db, []module.Module{ordinary, brokenUnselected}, MigrationRequest{
		Action: MigrationUp,
		Module: "ordinary",
	}, "")
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, "ordinary", reports[0].Module)
	assert.True(t, db.Migrator().HasTable("selected_ordinary"))
}

func TestMigrationMilestonesRejectDivergedHistoryBeforeWritingAnotherSource(t *testing.T) {
	db := openMilestoneSQLite(t)
	modules := milestoneTestModules()
	provider := modules[0].(module.MigrationSourceProvider)
	source := provider.MigrationSource()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, "sqlite", 1))
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+source.TableName+" (version_id, is_applied) VALUES (?, ?)", 3, true,
	)
	require.NoError(t, err)

	_, err = ExecuteModuleMigrations(t.Context(), db, modules, MigrationRequest{Action: MigrationUp}, "")
	assert.ErrorContains(t, err, "database contains migrations unavailable in embedded SQL: [3]")
	assert.False(t, db.Migrator().HasTable("milestone_b"), "all source histories must validate before the next source writes")
}

func TestSelectedMilestoneModuleRequiresExternalDependenciesBeforeItsBaseWrites(t *testing.T) {
	db := openMilestoneSQLite(t)
	_, err := ExecuteModuleMigrations(t.Context(), db, milestoneTestModules(), MigrationRequest{
		Action: MigrationUp,
		Module: "a",
	}, "")
	assert.ErrorContains(t, err, `milestone "a.constraints" requires unapplied milestone "b.base"`)
	assert.False(t, db.Migrator().HasTable("milestone_a"), "dependency preflight must happen before the selected source writes")
}

func TestInvalidMilestoneUpToTargetFailsBeforeOrdinarySourcesWrite(t *testing.T) {
	db := openMilestoneSQLite(t)
	ordinary := ordinaryMigrationTestModule{
		name: "ordinary",
		source: goosex.Source{TableName: "goose_ordinary", FS: fstest.MapFS{
			"sql/sqlite/00003_ordinary.sql": {Data: []byte(`-- +goose Up
CREATE TABLE premature_ordinary (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE premature_ordinary;
`)},
		}},
	}

	_, err := ExecuteModuleMigrations(t.Context(), db, append([]module.Module{ordinary}, milestoneTestModules()...), MigrationRequest{
		Action: MigrationUpTo,
		To:     3,
	}, "")
	assert.ErrorContains(t, err, "target 3 is not a migration milestone")
	assert.False(t, db.Migrator().HasTable("premature_ordinary"), "all milestone targets must validate before ordinary sources write")
}

func TestMigrationMilestoneDownProtectsPartiallyAppliedDependents(t *testing.T) {
	modules := []module.Module{
		newMilestoneTestModule("a", milestone("base", 1)),
		newMilestoneTestModule("b",
			milestone("base", 1, milestoneRef("a", "base")),
			milestone("constraints", 3, milestoneRef("a", "base")),
		),
	}
	plan, err := buildMigrationPlan(modules, "")
	require.NoError(t, err)
	plan.streamByName["a"].status.Current = 1
	plan.streamByName["b"].status.Current = 2

	err = plan.rejectAppliedDependents(map[migrationMilestoneKey]struct{}{
		{module: "a", name: "base"}: {},
	})
	assert.ErrorContains(t, err, `applied dependents: "b.base", "b.constraints"`)
}

func TestMigrationMilestoneGraphRejectsStartedNodeWithUnappliedDependency(t *testing.T) {
	plan, err := buildMigrationPlan(milestoneTestModules(), "")
	require.NoError(t, err)
	plan.streamByName["a"].status = goosex.Status{Target: 2, Embedded: []int64{1, 2}, Pending: []int64{1, 2}}
	plan.streamByName["b"].status = goosex.Status{
		Current:  1,
		Target:   2,
		Embedded: []int64{1, 2},
		Applied:  []int64{1},
		Pending:  []int64{2},
	}

	err = plan.validateMilestoneSources(plan.streams)
	assert.ErrorContains(t, err, `started milestone "b.base" requires unapplied milestone "a.base"`)
}

func openMilestoneSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir() + "/milestones.sqlite"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func milestoneTestModules() []module.Module {
	return []module.Module{
		newMilestoneTestModule("a",
			milestone("base", 1),
			milestone("constraints", 2, milestoneRef("b", "base")),
		),
		newMilestoneTestModule("b",
			milestone("base", 1, milestoneRef("a", "base")),
			milestone("constraints", 2, milestoneRef("a", "base")),
		),
	}
}

func newMilestoneTestModule(name string, milestones ...module.MigrationMilestone) milestoneTestModule {
	return milestoneTestModule{
		name:       name,
		milestones: milestones,
		source: goosex.Source{
			TableName: "goose_milestone_" + name,
			FS:        milestoneTestFS(name),
		},
	}
}

func milestone(name string, version int64, dependencies ...module.MigrationMilestoneRef) module.MigrationMilestone {
	return module.MigrationMilestone{Name: name, Version: version, Dependencies: dependencies}
}

func milestoneRef(moduleName, name string) module.MigrationMilestoneRef {
	return module.MigrationMilestoneRef{Module: moduleName, Name: name}
}

func milestoneTestFS(moduleName string) fstest.MapFS {
	if moduleName == "a" {
		return fstest.MapFS{
			"sql/sqlite/00001_base.sql": {Data: []byte(`-- +goose Up
CREATE TABLE milestone_trace (position INTEGER PRIMARY KEY AUTOINCREMENT, node TEXT NOT NULL);
CREATE TABLE milestone_a (id INTEGER PRIMARY KEY);
INSERT INTO milestone_a (id) VALUES (1);
INSERT INTO milestone_trace (node) VALUES ('a.base');
-- +goose Down
DELETE FROM milestone_trace WHERE node = 'a.base';
DROP TABLE milestone_a;
DROP TABLE milestone_trace;
`)},
			"sql/sqlite/00002_constraints.sql": {Data: []byte(`-- +goose Up
CREATE TABLE milestone_a_constraints (id INTEGER PRIMARY KEY);
INSERT INTO milestone_trace (node) SELECT 'a.constraints' FROM milestone_b LIMIT 1;
-- +goose Down
DELETE FROM milestone_trace WHERE node = 'a.constraints';
DROP TABLE milestone_a_constraints;
`)},
		}
	}
	return fstest.MapFS{
		"sql/sqlite/00001_base.sql": {Data: []byte(`-- +goose Up
CREATE TABLE milestone_b (id INTEGER PRIMARY KEY);
INSERT INTO milestone_b (id) VALUES (1);
INSERT INTO milestone_trace (node) VALUES ('b.base');
-- +goose Down
DELETE FROM milestone_trace WHERE node = 'b.base';
DROP TABLE milestone_b;
`)},
		"sql/sqlite/00002_constraints.sql": {Data: []byte(`-- +goose Up
CREATE TABLE milestone_b_constraints (id INTEGER PRIMARY KEY);
INSERT INTO milestone_trace (node) SELECT 'b.constraints' FROM milestone_a LIMIT 1;
-- +goose Down
DELETE FROM milestone_trace WHERE node = 'b.constraints';
DROP TABLE milestone_b_constraints;
`)},
	}
}

// TestMigrationDownTargetStepsBackOneMilestoneAtATime pins how far a rollback
// is allowed to go. A target computed too low would drop schema the operator
// never asked to remove, and one computed too high would leave the stream
// where it already is and report success without doing anything.
func TestMigrationDownTargetStepsBackOneMilestoneAtATime(t *testing.T) {
	stream := func(current int64) *migrationStream {
		return &migrationStream{
			name: "iam",
			milestones: []module.MigrationMilestone{
				{Name: "core", Version: 10},
				{Name: "directory", Version: 20},
				{Name: "governance", Version: 30},
			},
			status: goosex.Status{Current: current},
		}
	}

	// A plain "down" retreats to the milestone boundary below where it stands.
	for current, want := range map[int64]int64{
		0:  0,  // nothing applied, nothing to undo
		5:  0,  // mid-way into the first milestone
		10: 0,  // exactly on the first boundary, so the step goes below it
		15: 10, // past the first boundary
		30: 20, // exactly on the last boundary
		35: 30, // past the last boundary
	} {
		target, err := stream(current).downTarget(MigrationRequest{Action: MigrationDown})
		require.NoError(t, err, "current %d", current)
		assert.Equal(t, want, target, "down from current %d", current)
	}

	// down-to names its own target and must land on a declared milestone.
	target, err := stream(30).downTarget(MigrationRequest{Action: MigrationDownTo, To: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(10), target)

	target, err = stream(30).downTarget(MigrationRequest{Action: MigrationDownTo, To: 0})
	require.NoError(t, err)
	assert.Zero(t, target, "zero means undo everything and is always a legal target")

	// Refusing to "migrate down" to a higher version keeps the operator from
	// believing a rollback happened when the request was really an upgrade.
	_, err = stream(10).downTarget(MigrationRequest{Action: MigrationDownTo, To: 30})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "higher version")

	// A target between milestones would leave the schema in a state no module
	// declared, so it is refused rather than approximated.
	_, err = stream(30).downTarget(MigrationRequest{Action: MigrationDownTo, To: 25})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a migration milestone")
}

const (
	iamMigrationDatabaseType = "IAM_MIGRATION_DATABASE_TYPE"
	iamMigrationDatabaseDSN  = "IAM_MIGRATION_DATABASE_DSN"
	iamMigrationTablePrefix  = "IAM_MIGRATION_TABLE_PREFIX"
)

func TestIAMMigrationStatusExternalDatabase(t *testing.T) {
	databaseType := strings.TrimSpace(os.Getenv(iamMigrationDatabaseType))
	dsn := strings.TrimSpace(os.Getenv(iamMigrationDatabaseDSN))
	if databaseType == "" || dsn == "" {
		t.Skip("external IAM migration database is not configured")
	}
	config := iamMigrationStatusAppConfig(t, Database{
		Type: databaseType, Dsn: dsn, Writable: true, Readable: true,
		MaxOpenConns: 8, MaxIdleConns: 4,
	}, os.Getenv(iamMigrationTablePrefix))

	// Preserve the actual historical rollback contract before installing the
	// irreversible binding-trust milestone on this same database.
	historical := &App{config: config}
	require.NoError(t, configureApplication(&historical.config))
	require.NoError(t, historical.prepare(t.Context()))
	historicalClosed := false
	t.Cleanup(func() {
		if !historicalClosed {
			assert.NoError(t, historical.shutdown())
		}
	})
	owners := historical.modules.Modules()
	for index, owner := range owners {
		if owner.ModuleName() == federation.Name {
			owners[index] = historicalFederationStatusModule{Module: owner, source: historicalFederationStatusSource(t, owner)}
		}
	}
	runHistorical := func(request MigrationRequest) ([]MigrationReport, error) {
		return ExecuteModuleMigrations(t.Context(), historical.runtime.Database().Write(), owners, request, config.TablePrefix)
	}
	reports, err := runHistorical(MigrationRequest{Action: MigrationUp, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	reports, err = runHistorical(MigrationRequest{Action: MigrationStatus, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	// The retirement milestone depends on organization; remove it before its base.
	reports, err = runHistorical(MigrationRequest{Action: MigrationDownTo, Module: core.Name, To: 16})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, int64(16), reports[0].Status.Current)

	for _, name := range iamMigrationReverseOrder() {
		reports, err := runHistorical(MigrationRequest{Action: MigrationDownTo, Module: name, To: 0})
		require.NoError(t, err, "roll back %s", name)
		require.Len(t, reports, 1)
		assertMigrationReportState(t, reports[0], goosex.StatePending)
	}
	reports, err = runHistorical(MigrationRequest{Action: MigrationStatus, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StatePending)
	reports, err = runHistorical(MigrationRequest{Action: MigrationUp, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	require.NoError(t, historical.shutdown())
	historicalClosed = true

	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StateClean)
	assertIAMMigrationStatusEndpoint(t, config)
	// Downgrading past binding trust is refused, and refused before anything is
	// undone: the milestone declares itself irreversible, so the request never
	// reaches the database. The status assertion that follows is what proves it
	// -- a rollback that had started would leave federation between milestones.
	_, err = (&App{}).RunMigrations(t.Context(), MigrationConfig{
		AppConfig: config, MigrationRequest: MigrationRequest{Action: MigrationDownTo, Module: federation.Name, To: 6},
	})
	require.ErrorContains(t, err, "is irreversible")
	require.ErrorContains(t, err, "nothing was rolled back")
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StateClean)
	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
}

// historicalFederationVersion is the last federation migration this double
// presents. Both the milestone list and the embedded files are cut at it, so a
// migration added after it does not leave the source ahead of the milestones.
const historicalFederationVersion = 6

type historicalFederationStatusModule struct {
	module.Module
	source goosex.Source
}

func (m historicalFederationStatusModule) MigrationSource() goosex.Source { return m.source }

func (m historicalFederationStatusModule) MigrationMilestones() []module.MigrationMilestone {
	var milestones []module.MigrationMilestone
	for _, milestone := range m.Module.(module.MigrationMilestoneProvider).MigrationMilestones() {
		if milestone.Version <= historicalFederationVersion {
			milestones = append(milestones, milestone)
		}
	}
	return milestones
}

func historicalFederationStatusSource(t *testing.T, owner module.Module) goosex.Source {
	t.Helper()
	source := owner.(module.MigrationSourceProvider).MigrationSource()
	files := fstest.MapFS{}
	err := fs.WalkDir(source.FS, "sql", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		version, parseErr := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		require.NoError(t, parseErr, "migration file without a version prefix: %s", path)
		if version > historicalFederationVersion {
			return nil
		}
		contents, err := fs.ReadFile(source.FS, path)
		if err != nil {
			return err
		}
		files[path] = &fstest.MapFile{Data: contents}
		return nil
	})
	require.NoError(t, err)
	source.FS = files
	return source
}

func iamMigrationStatusAppConfig(t *testing.T, database Database, prefix string) AppConfig {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	config := validTestAppConfig()
	config.Secrets = testBusinessSecretsConfig(t)
	config.Name = "iam-migration-status-integration"
	config.TablePrefix = prefix
	config.Database = map[string]Database{"primary": database}
	config.API.MigrationStatus.Enabled = true
	config.Authn = IAMAuthn(authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", SigningKey: key, AccessTokenTTL: time.Hour,
		Web: authnext.WebConfig{
			Enabled: true, PostLoginURL: "https://app.example.test/home",
			PostLogoutURL:     "https://app.example.test/signed-out",
			AllowedReturnURLs: []string{"https://app.example.test/home"},
			AllowedOrigins:    []string{"https://app.example.test"},
		},
		MFA: authnext.MFAConfig{EncryptionKey: key},
	})
	config.Federation = IAMFederation{Enabled: true, EncryptionKey: key}
	config.Webhook = IAMWebhook{Enabled: true, EncryptionKey: key}
	return config
}

func assertIAMMigrationActionState(t *testing.T, config AppConfig, action MigrationAction, state goosex.State) {
	t.Helper()
	reports, err := (&App{}).RunMigrations(t.Context(), MigrationConfig{
		AppConfig: config, MigrationRequest: MigrationRequest{Action: action, To: -1},
	})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, state)
}

func assertIAMMigrationReports(t *testing.T, reports []MigrationReport, state goosex.State) {
	t.Helper()
	expected := iamMigrationFlowNames()
	require.Len(t, reports, len(expected))
	names := make([]string, 0, len(reports))
	for _, report := range reports {
		names = append(names, report.Module)
		assertMigrationReportState(t, report, state)
	}
	sort.Strings(names)
	sort.Strings(expected)
	assert.Equal(t, expected, names)
}

func assertMigrationReportState(t *testing.T, report MigrationReport, state goosex.State) {
	t.Helper()
	assert.Equal(t, state, report.Status.State, report.Module)
	assert.Empty(t, report.Status.Unknown, report.Module)
	switch state {
	case goosex.StateClean:
		assert.Equal(t, report.Status.Target, report.Status.Current, report.Module)
		assert.Equal(t, report.Status.Embedded, report.Status.Applied, report.Module)
		assert.Empty(t, report.Status.Pending, report.Module)
	case goosex.StatePending:
		assert.Zero(t, report.Status.Current, report.Module)
		assert.Empty(t, report.Status.Applied, report.Module)
		assert.Equal(t, report.Status.Embedded, report.Status.Pending, report.Module)
	}
}

func assertIAMMigrationStatusEndpoint(t *testing.T, config AppConfig) {
	t.Helper()
	application := &App{config: config}
	require.NoError(t, application.bootstrap(t.Context()))
	closed := false
	t.Cleanup(func() {
		if !closed {
			assert.NoError(t, application.shutdown())
		}
	})
	router, api := moduleRouter(t, application)

	for _, path := range []string{migrationstatus.Path, migrationstatus.LegacyPath} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code, path)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"), path)
		var envelope respx.Envelope[struct {
			State   goosex.State             `json:"state"`
			Modules []migrationstatus.Report `json:"modules"`
		}]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		assert.Zero(t, envelope.Code)
		assert.Equal(t, goosex.StateClean, envelope.Data.State)
		reports := make([]MigrationReport, len(envelope.Data.Modules))
		for index, report := range envelope.Data.Modules {
			reports[index] = MigrationReport{Module: report.Module, Status: report.Status}
		}
		assertIAMMigrationReports(t, reports, goosex.StateClean)
	}
	assert.Contains(t, api.OpenAPI().Paths, migrationstatus.Path)
	assert.Contains(t, api.OpenAPI().Paths, migrationstatus.LegacyPath)
	assert.True(t, api.OpenAPI().Paths[migrationstatus.LegacyPath].Get.Deprecated)
	require.NoError(t, application.shutdown())
	closed = true
}

func iamMigrationFlowNames() []string {
	return []string{
		dlock.Name, wuid.Name, infraaudit.Name, iamaudit.Name, core.Name, files.Name, iamauthn.Name, identity.Name,
		organization.Name, oauth.Name, provisioning.Name, governance.Name, federation.Name, webhook.Name, todo.Name,
		events.Name, idempotency.Name, jobs.Name, intake.Name, fleet.Name, collab.Name, agents.Name, knowledge.Name, businessgovernance.Name, delivery.Name, performance.Name, planning.Name, templates.Name, yunxiao.Name, clientwork.Name, budget.Name, execution.Name, notification.Name,
	}
}

func iamMigrationReverseOrder() []string {
	return []string{
		// Business aggregates come down before the IAM entities they reference.
		notification.Name, execution.Name, budget.Name, clientwork.Name, yunxiao.Name, templates.Name, planning.Name, performance.Name, delivery.Name, businessgovernance.Name, knowledge.Name, agents.Name, collab.Name, fleet.Name, intake.Name,
		// Webhook comes down before its IAM dependencies: it only reads what they record, so
		// nothing depends on it and removing it frees the chain it follows.
		webhook.Name, federation.Name, iamauthn.Name, oauth.Name, governance.Name, provisioning.Name,
		organization.Name, files.Name, core.Name, identity.Name, iamaudit.Name,
		todo.Name, jobs.Name, infraaudit.Name, idempotency.Name, events.Name, wuid.Name, dlock.Name,
	}
}
