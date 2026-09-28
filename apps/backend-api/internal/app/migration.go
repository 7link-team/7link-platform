package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	configx "7link.com/platform/pkg/config"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type MigrationAction string

const (
	MigrationAuto     MigrationAction = "auto"
	MigrationStatus   MigrationAction = "status"
	MigrationValidate MigrationAction = "validate"
	MigrationUp       MigrationAction = "up"
	MigrationUpTo     MigrationAction = "up-to"
	MigrationDown     MigrationAction = "down"
	MigrationDownTo   MigrationAction = "down-to"
)

// MigrationConfig combines the application's normal configuration with the
// migration command's operation-only settings. AppConfig is squashed so serve
// and migrate read the same top-level configuration shape.
type MigrationConfig struct {
	AppConfig        `mapstructure:",squash"`
	MigrationRequest `mapstructure:",squash"`
}

// MigrationRequest contains only one migration operation's control data.
type MigrationRequest struct {
	Action MigrationAction `mapstructure:"-" hidden:"true"`
	Module string          `mapstructure:"module" description:"limit the operation to one module" trim:"true"`
	To     int64           `mapstructure:"to" description:"target version for up-to or down-to" default:"-1"`
}

type MigrationReport struct {
	Module string `json:"module"`
	// Milestone is the last fully applied named milestone. It is empty for an
	// ordinary migration source or before the first milestone is complete.
	Milestone string        `json:"milestone,omitempty"`
	Status    goosex.Status `json:"status"`
}

// RunMigrations executes one migration command without initializing or starting
// application modules. It composes and prepares the same Module Registry as the
// service path, executes only the migration phase, and shuts down through the
// normal application cleanup path.
func (app *App) RunMigrations(ctx context.Context, config MigrationConfig) (reports []MigrationReport, resultErr error) {
	if err := configx.Process(&config.MigrationRequest); err != nil {
		return nil, err
	}
	if err := configureApplication(&config.AppConfig); err != nil {
		return nil, err
	}
	app.config = config.AppConfig
	defer func() {
		resultErr = errors.Join(resultErr, app.shutdown())
	}()
	if err := app.prepare(ctx); err != nil {
		return nil, fmt.Errorf("prepare migration application: %w", err)
	}
	return app.migrateModules(ctx, config.MigrationRequest)
}

// migrateModules executes one migration action against this application's active
// Module Registry. Runtime database ownership and Module discovery never enter
// through a second command-specific path.
func (app *App) migrateModules(ctx context.Context, request MigrationRequest) ([]MigrationReport, error) {
	if app.runtime == nil || app.runtime.Database() == nil {
		return nil, errors.New("migration: writable database is not configured")
	}
	db := app.runtime.Database().Write()
	if db == nil {
		return nil, errors.New("migration: writable database is not configured")
	}
	if app.modules == nil {
		return nil, errors.New("migration: modules are not composed")
	}
	return executeModuleMigrations(ctx, db, app.modules.Modules(), request, app.runtime.TablePrefix())
}

func executeModuleMigrations(
	ctx context.Context,
	db *gorm.DB,
	modules []module.Module,
	request MigrationRequest,
	prefix string,
) ([]MigrationReport, error) {
	return ExecuteModuleMigrations(ctx, db, modules, request, prefix)
}

// ExecuteModuleMigrations runs one migration action for a prepared module set.
// It is exported from this internal package so the separate integration-test
// module can exercise the production scheduler against all database engines.
func ExecuteModuleMigrations(
	ctx context.Context,
	db *gorm.DB,
	modules []module.Module,
	request MigrationRequest,
	prefix string,
) ([]MigrationReport, error) {
	if db == nil {
		return nil, errors.New("migration: writable database is unavailable")
	}
	action := request.Action
	if !slices.Contains([]MigrationAction{
		MigrationAuto,
		MigrationStatus,
		MigrationValidate,
		MigrationUp,
		MigrationUpTo,
		MigrationDown,
		MigrationDownTo,
	}, action) {
		return nil, fmt.Errorf("migration: unsupported action %q", action)
	}
	if slices.Contains([]MigrationAction{MigrationDown, MigrationDownTo}, action) && request.Module == "" {
		return nil, errors.New("migration: down requires a module")
	}
	if slices.Contains([]MigrationAction{MigrationUpTo, MigrationDownTo}, action) && request.To < 0 {
		return nil, errors.New("migration: target version cannot be negative")
	}
	plan, err := buildMigrationPlan(modules, prefix)
	if err != nil {
		return nil, err
	}
	selected, found := plan.selectStreams(request.Module)
	if !found {
		return nil, fmt.Errorf("migration: module %q is not registered", request.Module)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("migration: get database pool: %w", err)
	}
	dialect := db.Dialector.Name()
	inspectionSet := selected
	if request.Module == "" || (len(selected) == 1 && len(selected[0].milestones) > 0) {
		inspectionSet = plan.streamsNeededForMilestones(selected)
	}
	if err := inspectMigrationStreams(ctx, sqlDB, dialect, inspectionSet); err != nil {
		return nil, err
	}
	if err := plan.validateMilestoneSources(inspectionSet); err != nil {
		return nil, err
	}
	if _, err := plan.desiredMilestones(selected, request); err != nil {
		return nil, err
	}

	var errs []error
	if action != MigrationStatus && action != MigrationValidate {
		for _, stream := range selected {
			if len(stream.milestones) != 0 {
				continue
			}
			if migrationErr := executeOrdinaryMigration(ctx, sqlDB, dialect, stream.source, request); migrationErr != nil {
				errs = append(errs, fmt.Errorf("%s migration %s: %w", stream.name, action, migrationErr))
			}
		}
		if milestoneErr := plan.executeMilestones(ctx, sqlDB, dialect, selected, request); milestoneErr != nil {
			errs = append(errs, milestoneErr)
		}
	}

	reports := make([]MigrationReport, 0, len(selected))
	for _, stream := range selected {
		status, inspectErr := goosex.Inspect(ctx, sqlDB, stream.source, dialect)
		if inspectErr != nil {
			errs = append(errs, fmt.Errorf("%s migration status: %w", stream.name, inspectErr))
			continue
		}
		stream.status = status
		if action == MigrationValidate && status.State != goosex.StateClean {
			errs = append(errs, fmt.Errorf("%s migration state is %s", stream.name, status.State))
		}
		reports = append(reports, MigrationReport{
			Module:    stream.name,
			Milestone: stream.currentMilestone(),
			Status:    status,
		})
	}
	return reports, errors.Join(errs...)
}

type migrationStream struct {
	name       string
	source     goosex.Source
	milestones []module.MigrationMilestone
	status     goosex.Status
}

type migrationMilestoneKey struct {
	module string
	name   string
}

func (key migrationMilestoneKey) String() string {
	return key.module + "." + key.name
}

type migrationMilestoneNode struct {
	key          migrationMilestoneKey
	version      int64
	dependencies []migrationMilestoneKey
	stream       *migrationStream
}

type migrationPlan struct {
	streams      []*migrationStream
	streamByName map[string]*migrationStream
	nodes        []*migrationMilestoneNode
	nodeByKey    map[migrationMilestoneKey]*migrationMilestoneNode
	topological  []*migrationMilestoneNode
}

func buildMigrationPlan(modules []module.Module, prefix string) (*migrationPlan, error) {
	plan := &migrationPlan{
		streamByName: make(map[string]*migrationStream),
		nodeByKey:    make(map[migrationMilestoneKey]*migrationMilestoneNode),
	}
	for _, appModule := range modules {
		provider, ok := appModule.(module.MigrationSourceProvider)
		if !ok {
			continue
		}
		name := appModule.ModuleName()
		if _, exists := plan.streamByName[name]; exists {
			return nil, fmt.Errorf("migration: duplicate migration module %q", name)
		}
		source := provider.MigrationSource()
		// The prefix is an application-level schema property, so it is applied
		// here rather than by each module: one injection point cannot drift.
		source.Prefix = prefix
		stream := &migrationStream{name: name, source: source}
		if milestoneProvider, milestoneAware := appModule.(module.MigrationMilestoneProvider); milestoneAware {
			stream.milestones = milestoneProvider.MigrationMilestones()
			if len(stream.milestones) == 0 {
				return nil, fmt.Errorf("migration: module %q provides no migration milestones", name)
			}
		}
		plan.streams = append(plan.streams, stream)
		plan.streamByName[name] = stream
		if err := plan.addMilestoneNodes(stream); err != nil {
			return nil, err
		}
	}
	if err := plan.resolveMilestoneGraph(); err != nil {
		return nil, err
	}
	return plan, nil
}

func (plan *migrationPlan) addMilestoneNodes(stream *migrationStream) error {
	var previous *migrationMilestoneNode
	for _, declaration := range stream.milestones {
		if declaration.Name == "" {
			return fmt.Errorf("migration: module %q has an empty migration milestone name", stream.name)
		}
		if declaration.Version <= 0 {
			return fmt.Errorf("migration: milestone %q must have a positive version", stream.name+"."+declaration.Name)
		}
		if previous != nil && declaration.Version <= previous.version {
			return fmt.Errorf(
				"migration: migration milestones for module %q must have strictly increasing versions: %d then %d",
				stream.name,
				previous.version,
				declaration.Version,
			)
		}
		key := migrationMilestoneKey{module: stream.name, name: declaration.Name}
		if _, exists := plan.nodeByKey[key]; exists {
			return fmt.Errorf("migration: duplicate migration milestone %q", key.String())
		}
		node := &migrationMilestoneNode{key: key, version: declaration.Version, stream: stream}
		seenDependencies := make(map[migrationMilestoneKey]struct{})
		if previous != nil {
			node.dependencies = append(node.dependencies, previous.key)
			seenDependencies[previous.key] = struct{}{}
		}
		for _, dependency := range declaration.Dependencies {
			dependencyKey := migrationMilestoneKey{module: dependency.Module, name: dependency.Name}
			if _, exists := seenDependencies[dependencyKey]; exists {
				continue
			}
			node.dependencies = append(node.dependencies, dependencyKey)
			seenDependencies[dependencyKey] = struct{}{}
		}
		plan.nodes = append(plan.nodes, node)
		plan.nodeByKey[key] = node
		previous = node
	}
	return nil
}

func (plan *migrationPlan) resolveMilestoneGraph() error {
	indegree := make(map[migrationMilestoneKey]int, len(plan.nodes))
	dependents := make(map[migrationMilestoneKey][]*migrationMilestoneNode, len(plan.nodes))
	for _, node := range plan.nodes {
		indegree[node.key] = len(node.dependencies)
		for _, dependency := range node.dependencies {
			if _, exists := plan.nodeByKey[dependency]; !exists {
				return fmt.Errorf(
					"migration: migration milestone %q requires unknown milestone %q",
					node.key.String(),
					dependency.String(),
				)
			}
			dependents[dependency] = append(dependents[dependency], node)
		}
	}
	ready := make([]*migrationMilestoneNode, 0, len(plan.nodes))
	for _, node := range plan.nodes {
		if indegree[node.key] == 0 {
			ready = append(ready, node)
		}
	}
	for len(ready) > 0 {
		node := ready[0]
		ready = ready[1:]
		plan.topological = append(plan.topological, node)
		for _, dependent := range dependents[node.key] {
			indegree[dependent.key]--
			if indegree[dependent.key] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(plan.topological) == len(plan.nodes) {
		return nil
	}
	cycle := make([]migrationMilestoneKey, 0)
	for _, node := range plan.nodes {
		if indegree[node.key] > 0 {
			cycle = append(cycle, node.key)
		}
	}
	sortMilestoneKeys(cycle)
	return fmt.Errorf("migration: migration milestone dependency cycle: %s", formatMilestoneKeys(cycle))
}

func (plan *migrationPlan) selectStreams(moduleName string) ([]*migrationStream, bool) {
	if moduleName == "" {
		return plan.streams, true
	}
	stream, found := plan.streamByName[moduleName]
	if !found {
		return nil, false
	}
	return []*migrationStream{stream}, true
}

func (plan *migrationPlan) streamsNeededForMilestones(selected []*migrationStream) []*migrationStream {
	needsMilestones := false
	for _, stream := range selected {
		if len(stream.milestones) > 0 {
			needsMilestones = true
			break
		}
	}
	if !needsMilestones {
		return selected
	}
	streams := make([]*migrationStream, 0, len(plan.streams))
	for _, stream := range plan.streams {
		if len(stream.milestones) > 0 || slices.Contains(selected, stream) {
			streams = append(streams, stream)
		}
	}
	return streams
}

func inspectMigrationStreams(ctx context.Context, db *sql.DB, dialect string, streams []*migrationStream) error {
	for _, stream := range streams {
		status, err := goosex.Inspect(ctx, db, stream.source, dialect)
		if err != nil {
			return fmt.Errorf("%s migration status: %w", stream.name, err)
		}
		stream.status = status
	}
	return nil
}

func (plan *migrationPlan) validateMilestoneSources(streams []*migrationStream) error {
	inspected := make(map[*migrationStream]struct{}, len(streams))
	for _, stream := range streams {
		inspected[stream] = struct{}{}
		if len(stream.milestones) == 0 {
			continue
		}
		if err := goosex.ValidateStatus(stream.status); err != nil {
			return fmt.Errorf("%s migration state: %w", stream.name, err)
		}
		for _, milestone := range stream.milestones {
			if !slices.Contains(stream.status.Embedded, milestone.Version) {
				return fmt.Errorf(
					"migration: milestone %q version %d is not embedded in its source",
					stream.name+"."+milestone.Name,
					milestone.Version,
				)
			}
		}
		last := stream.milestones[len(stream.milestones)-1]
		if last.Version != stream.status.Target {
			return fmt.Errorf(
				"migration: final milestone %q ends at version %d, but source target is %d",
				stream.name+"."+last.Name,
				last.Version,
				stream.status.Target,
			)
		}
	}
	for _, node := range plan.topological {
		if _, wasInspected := inspected[node.stream]; !wasInspected || !plan.nodeStarted(node) {
			continue
		}
		for _, dependency := range node.dependencies {
			dependencyNode := plan.nodeByKey[dependency]
			if _, wasInspected := inspected[dependencyNode.stream]; wasInspected && !plan.nodeApplied(dependency) {
				return fmt.Errorf(
					"migration: started milestone %q requires unapplied milestone %q",
					node.key.String(),
					dependency.String(),
				)
			}
		}
	}
	return nil
}

func executeOrdinaryMigration(
	ctx context.Context,
	db *sql.DB,
	dialect string,
	source goosex.Source,
	request MigrationRequest,
) error {
	switch request.Action {
	case MigrationAuto:
		return goosex.Migrate(ctx, db, source, dialect)
	case MigrationUp:
		return goosex.MigrateUp(ctx, db, source, dialect)
	case MigrationUpTo:
		return goosex.MigrateUpTo(ctx, db, source, dialect, request.To)
	case MigrationDown:
		return goosex.MigrateDown(ctx, db, source, dialect)
	case MigrationDownTo:
		return goosex.MigrateDownTo(ctx, db, source, dialect, request.To)
	default:
		return nil
	}
}

func (plan *migrationPlan) executeMilestones(
	ctx context.Context,
	db *sql.DB,
	dialect string,
	selected []*migrationStream,
	request MigrationRequest,
) error {
	milestoneStreams := make([]*migrationStream, 0, len(selected))
	for _, stream := range selected {
		if len(stream.milestones) > 0 {
			milestoneStreams = append(milestoneStreams, stream)
		}
	}
	if len(milestoneStreams) == 0 {
		return nil
	}
	switch request.Action {
	case MigrationAuto, MigrationUp, MigrationUpTo:
		return plan.migrateMilestonesUp(ctx, db, dialect, milestoneStreams, request)
	case MigrationDown, MigrationDownTo:
		return plan.migrateMilestonesDown(ctx, db, dialect, selected[0], request)
	default:
		return nil
	}
}

func (plan *migrationPlan) migrateMilestonesUp(
	ctx context.Context,
	db *sql.DB,
	dialect string,
	selected []*migrationStream,
	request MigrationRequest,
) error {
	desired, err := plan.desiredMilestones(selected, request)
	if err != nil {
		return err
	}
	for _, node := range plan.topological {
		if _, included := desired[node.key]; !included || plan.nodeApplied(node.key) {
			continue
		}
		if err := goosex.MigrateUpTo(ctx, db, node.stream.source, dialect, node.version); err != nil {
			return fmt.Errorf(
				"%s migration %s milestone %q: %w",
				node.stream.name,
				request.Action,
				node.key.String(),
				err,
			)
		}
		node.stream.status.Current = node.version
	}
	return nil
}

func (plan *migrationPlan) desiredMilestones(
	selected []*migrationStream,
	request MigrationRequest,
) (map[migrationMilestoneKey]struct{}, error) {
	milestoneStreams := make(map[*migrationStream]struct{}, len(selected))
	for _, stream := range selected {
		if len(stream.milestones) > 0 {
			milestoneStreams[stream] = struct{}{}
		}
	}
	if request.Action != MigrationAuto && request.Action != MigrationUp && request.Action != MigrationUpTo {
		return nil, nil
	}
	return plan.desiredMilestonesMap(milestoneStreams, request)
}

func (plan *migrationPlan) desiredMilestonesMap(
	selected map[*migrationStream]struct{},
	request MigrationRequest,
) (map[migrationMilestoneKey]struct{}, error) {
	desired := make(map[migrationMilestoneKey]struct{})
	for _, stream := range plan.streams {
		if _, included := selected[stream]; !included {
			continue
		}
		target := stream.milestones[len(stream.milestones)-1].Version
		if request.Action == MigrationUpTo {
			target = request.To
			if target != 0 && !stream.hasMilestoneVersion(target) {
				return nil, fmt.Errorf("%s migration up-to: target %d is not a migration milestone", stream.name, target)
			}
		}
		if target < stream.status.Current {
			return nil, fmt.Errorf(
				"%s migration %s: cannot migrate up from %d to lower version %d",
				stream.name,
				request.Action,
				stream.status.Current,
				target,
			)
		}
		for _, node := range plan.nodes {
			if node.stream == stream && node.version <= target {
				desired[node.key] = struct{}{}
			}
		}
	}
	for _, node := range plan.topological {
		if _, included := desired[node.key]; !included {
			continue
		}
		for _, dependency := range node.dependencies {
			if _, included := desired[dependency]; included || plan.nodeApplied(dependency) {
				continue
			}
			return nil, fmt.Errorf(
				"%s migration %s: milestone %q requires unapplied milestone %q",
				node.stream.name,
				request.Action,
				node.key.String(),
				dependency.String(),
			)
		}
	}
	return desired, nil
}

func (plan *migrationPlan) migrateMilestonesDown(
	ctx context.Context,
	db *sql.DB,
	dialect string,
	stream *migrationStream,
	request MigrationRequest,
) error {
	target, err := stream.downTarget(request)
	if err != nil {
		return err
	}
	if target == stream.status.Current {
		return nil
	}
	removed := make(map[migrationMilestoneKey]struct{})
	for _, node := range plan.nodes {
		if node.stream == stream && node.version > target && node.version <= stream.status.Current {
			removed[node.key] = struct{}{}
		}
	}
	if err := plan.rejectAppliedDependents(removed); err != nil {
		return fmt.Errorf("%s migration %s: %w", stream.name, request.Action, err)
	}

	currentBoundary := stream.currentMilestoneVersion()
	if stream.status.Current > currentBoundary {
		if err := goosex.MigrateDownTo(ctx, db, stream.source, dialect, currentBoundary); err != nil {
			return fmt.Errorf("%s migration %s to milestone boundary %d: %w", stream.name, request.Action, currentBoundary, err)
		}
		stream.status.Current = currentBoundary
	}
	for index := len(plan.topological) - 1; index >= 0; index-- {
		node := plan.topological[index]
		if _, shouldRemove := removed[node.key]; !shouldRemove {
			continue
		}
		previous := stream.previousMilestoneVersion(node.version)
		if previous < target {
			previous = target
		}
		if err := goosex.MigrateDownTo(ctx, db, stream.source, dialect, previous); err != nil {
			return fmt.Errorf(
				"%s migration %s milestone %q: %w",
				stream.name,
				request.Action,
				node.key.String(),
				err,
			)
		}
		stream.status.Current = previous
	}
	return nil
}

func (stream *migrationStream) downTarget(request MigrationRequest) (int64, error) {
	if request.Action == MigrationDownTo {
		if request.To > stream.status.Current {
			return 0, fmt.Errorf(
				"%s migration down-to: cannot migrate down from %d to higher version %d",
				stream.name,
				stream.status.Current,
				request.To,
			)
		}
		if request.To != 0 && !stream.hasMilestoneVersion(request.To) {
			return 0, fmt.Errorf("%s migration down-to: target %d is not a migration milestone", stream.name, request.To)
		}
		return request.To, nil
	}
	if stream.status.Current == 0 {
		return 0, nil
	}
	currentBoundary := stream.currentMilestoneVersion()
	if stream.status.Current > currentBoundary {
		return currentBoundary, nil
	}
	return stream.previousMilestoneVersion(currentBoundary), nil
}

func (plan *migrationPlan) rejectAppliedDependents(removed map[migrationMilestoneKey]struct{}) error {
	if len(removed) == 0 {
		return nil
	}
	blocked := make([]migrationMilestoneKey, 0)
	for _, node := range plan.nodes {
		if _, removing := removed[node.key]; removing || !plan.nodeStarted(node) {
			continue
		}
		for _, dependency := range node.dependencies {
			if _, removingDependency := removed[dependency]; removingDependency {
				blocked = append(blocked, node.key)
				break
			}
		}
	}
	if len(blocked) == 0 {
		return nil
	}
	removedKeys := make([]migrationMilestoneKey, 0, len(removed))
	for key := range removed {
		removedKeys = append(removedKeys, key)
	}
	sortMilestoneKeys(removedKeys)
	sortMilestoneKeys(blocked)
	noun := "milestone"
	if len(removedKeys) > 1 {
		noun = "milestones"
	}
	return fmt.Errorf(
		"cannot roll back migration %s %s; applied dependents: %s",
		noun,
		formatMilestoneKeys(removedKeys),
		formatMilestoneKeys(blocked),
	)
}

func (plan *migrationPlan) nodeApplied(key migrationMilestoneKey) bool {
	node := plan.nodeByKey[key]
	return node != nil && node.stream.status.Current >= node.version
}

func (plan *migrationPlan) nodeStarted(node *migrationMilestoneNode) bool {
	return node != nil && node.stream.status.Current > node.stream.previousMilestoneVersion(node.version)
}

func (stream *migrationStream) hasMilestoneVersion(version int64) bool {
	for _, milestone := range stream.milestones {
		if milestone.Version == version {
			return true
		}
	}
	return false
}

func (stream *migrationStream) currentMilestone() string {
	var current string
	for _, milestone := range stream.milestones {
		if milestone.Version > stream.status.Current {
			break
		}
		current = milestone.Name
	}
	return current
}

func (stream *migrationStream) currentMilestoneVersion() int64 {
	var current int64
	for _, milestone := range stream.milestones {
		if milestone.Version > stream.status.Current {
			break
		}
		current = milestone.Version
	}
	return current
}

func (stream *migrationStream) previousMilestoneVersion(version int64) int64 {
	var previous int64
	for _, milestone := range stream.milestones {
		if milestone.Version >= version {
			break
		}
		previous = milestone.Version
	}
	return previous
}

func sortMilestoneKeys(keys []migrationMilestoneKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].module == keys[j].module {
			return keys[i].name < keys[j].name
		}
		return keys[i].module < keys[j].module
	})
}

func formatMilestoneKeys(keys []migrationMilestoneKey) string {
	formatted := make([]string, len(keys))
	for index, key := range keys {
		formatted[index] = fmt.Sprintf("%q", key.String())
	}
	return strings.Join(formatted, ", ")
}
