package app

import (
	"testing"
	"testing/fstest"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
