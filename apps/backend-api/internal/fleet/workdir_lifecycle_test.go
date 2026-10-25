package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func workdirLifecycleFixture(t *testing.T) (*Module, *gorm.DB, context.Context, MachineWorkdirView, *fleetPublisher) {
	t.Helper()
	m, db, ctx, _, publisher := enrollmentHarness(t)
	m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.workdirQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	var row MachineWorkdirView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "D:/repo")
		return err
	}))
	return m, db, ctx, row, publisher
}

func TestWorkdirLifecycleEditArchiveRestoreAndCurrentResolution(t *testing.T) {
	m, db, ctx, row, _ := workdirLifecycleFixture(t)
	change := func(version int, in WorkdirChange) MachineWorkdirView {
		var out MachineWorkdirView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.changeWorkdirOn(ctx, tx, 7, 1, row.ID, version, in)
			return e
		}))
		return out
	}
	require.Equal(t, 1, row.Version)
	changed := change(1, WorkdirChange{Path: "D:/new"})
	require.Equal(t, 2, changed.Version)
	require.Equal(t, "D:/new", changed.Path)
	archived := change(2, WorkdirChange{Path: "D:/new", Archived: true})
	require.NotNil(t, archived.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.ResolveWorkspaceOn(ctx, tx, 7, 1, 10, 11)
		require.ErrorIs(t, e, ErrMachineNotFound)
		_, e = m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 11, "D:/new")
		require.ErrorIs(t, e, ErrVersionConflict)
		_, e = m.requestScopedInspectionOn(ctx, tx, 7, 1, "CAPABILITIES", row.ID)
		require.ErrorIs(t, e, ErrMachineNotFound)
		facts, e := m.capabilitiesOn(ctx, tx, 7, 1)
		require.NoError(t, e)
		require.Len(t, facts.Workdirs, 1)
		require.NotNil(t, facts.Workdirs[0].ArchivedAt)
		return nil
	}))
	restored := change(3, WorkdirChange{Path: "D:/new"})
	require.Nil(t, restored.ArchivedAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, e := m.ResolveWorkspaceOn(ctx, tx, 7, 1, 10, 11)
		require.NoError(t, e)
		require.Equal(t, "D:/new", fact.Path)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeWorkdirOn(ctx, tx, 7, 1, row.ID, 3, WorkdirChange{Path: "D:/old"})
		return e
	}), ErrVersionConflict)
}

func TestWorkdirLifecycleDenialsAndTransactionalRollback(t *testing.T) {
	for _, fault := range []string{"execution", "inspection", "guard_missing", "guard_error", "repository", "path", "collision", "audit", "outbox", "foreign"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, publisher := workdirLifecycleFixture(t)
			in := WorkdirChange{Path: "D:/new", Archived: true}
			tenant := guid.ID(7)
			failure := errors.New("workspace mutation unavailable")
			switch fault {
			case "inspection":
				q := query.BindInspections(db, m.names)
				require.NoError(t, q.WithContext(ctx).Create(&model.Inspection{ID: 991, TenantID: 7, MachineID: 1, ConnEpoch: 1, Kind: "CAPABILITIES", ScopeKey: row.ID.String(), State: "QUEUED", WorkdirPath: row.Path}))
			case "execution":
				m.workdirQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
			case "guard_missing":
				m.workdirQuiet = nil
			case "guard_error":
				m.workdirQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, failure }
			case "repository":
				m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrMachineForbidden }
			case "path":
				in.Path = "D:/../outside"
			case "collision":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 12, "D:/new"); return e }))
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				publisher.fail = true
			case "foreign":
				tenant = 8
			}
			var before, after int64
			require.NoError(t, db.Table("test_fleet_effects").Count(&before).Error)
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.changeWorkdirOn(ctx, tx, tenant, 1, row.ID, 1, in); return e }))
			q := query.BindWorkdirs(db, m.names)
			saved, e := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, e)
			require.Equal(t, row, workdirView(saved))
			require.NoError(t, db.Table("test_fleet_effects").Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestWorkdirLifecycleHTTPReplayCASAndCurrentRepositoryFence(t *testing.T) {
	m, db, _, row, _ := workdirLifecycleFixture(t)
	store := idempotency.NewModule(m.runtime)
	raw, e := db.DB()
	require.NoError(t, e)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, m.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, store.InjectServices(services))
	m.idempotency = store
	router := fleetBusinessRouter(t, m)
	url := fmt.Sprintf("/machines/1/workdirs/%s", row.ID)
	headers := map[string]string{"If-Match": "1", "Idempotency-Key": "workspace-edit-1"}
	body := `{"path":"D:/new","archived":false}`
	first := fleetHTTPRequest(router, http.MethodPut, url, body, headers)
	require.Equal(t, 200, first.Code, first.Body.String())
	var expected, actual struct {
		Data MachineWorkdirView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &expected))
	require.Equal(t, 2, expected.Data.Version)
	replay := fleetHTTPRequest(router, http.MethodPut, url, body, headers)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
	response := fleetHTTPRequest(router, http.MethodPut, url, `{"path":"D:/different","archived":false}`, headers)
	require.Equal(t, 409, response.Code, response.Body.String())
	response = fleetHTTPRequest(router, http.MethodPut, url, body, map[string]string{"If-Match": "1", "Idempotency-Key": "workspace-edit-2"})
	require.Equal(t, 409, response.Code, response.Body.String())
	m.repository = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrMachineForbidden }
	response = fleetHTTPRequest(router, http.MethodPut, url, body, headers)
	require.Equal(t, 403, response.Code, response.Body.String())
}

func TestWorkdirLifecycleConnectorFailsClosed(t *testing.T) {
	m := &Module{}
	require.ErrorIs(t, m.bindWorkdirQuiet(nil), ErrInvalidArgument)
	guard := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.NoError(t, m.bindWorkdirQuiet(guard))
	require.ErrorIs(t, m.bindWorkdirQuiet(guard), ErrInvalidArgument)
	require.ErrorIs(t, (*Module)(nil).bindWorkdirQuiet(guard), ErrInvalidArgument)
}

func TestWorkdirLifecycleHidesStaleAndArchivedProbeFacts(t *testing.T) {
	m, db, ctx, row, _ := workdirLifecycleFixture(t)
	q := query.BindInspections(db, m.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.Inspection{ID: 992, TenantID: 7, MachineID: 1, ConnEpoch: 1, Kind: "CAPABILITIES", ScopeKey: row.ID.String(), State: "READY", WorkdirPath: row.Path, ResultJSON: "[]"}))
	read := func(want int) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			facts, err := m.capabilitiesOn(ctx, tx, 7, 1)
			require.NoError(t, err)
			require.Len(t, facts.Discoveries, want)
			return nil
		}))
	}
	read(1)
	var changed MachineWorkdirView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = m.changeWorkdirOn(ctx, tx, 7, 1, row.ID, 1, WorkdirChange{Path: "D:/new"})
		return err
	}))
	read(0)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(guid.ID(992))).Update(q.WorkdirPath, changed.Path)
	require.NoError(t, err)
	read(1)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeWorkdirOn(ctx, tx, 7, 1, row.ID, 2, WorkdirChange{Path: changed.Path, Archived: true})
		return err
	}))
	read(0)
}

func TestWorkdirMutationEveryStorageFailurePreservesPathRevisionAndEffects(t *testing.T) {
	for _, command := range []string{"register", "change"} {
		for _, kind := range []string{"query", "create", "update"} {
			exercise := func(t *testing.T, failAt int) int {
				m, db, ctx, row, _ := workdirLifecycleFixture(t)
				q := query.BindWorkdirs(db, m.names)
				before, err := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, err)
				var effects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&effects).Error)
				count := 0
				injected := errors.New("workspace storage unavailable")
				hook := func(tx *gorm.DB) {
					count++
					if count == failAt {
						tx.AddError(injected)
					}
				}
				switch kind {
				case "query":
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("workdir_mutation_failure", hook))
				case "create":
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("workdir_mutation_failure", hook))
				case "update":
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("workdir_mutation_failure", hook))
				}
				var out MachineWorkdirView
				err = db.Transaction(func(tx *gorm.DB) error {
					var e error
					if command == "register" {
						out, e = m.RegisterWorkdirOn(ctx, tx, 7, 1, 10, 12, "D:/other")
					} else {
						out, e = m.changeWorkdirOn(ctx, tx, 7, 1, row.ID, 1, WorkdirChange{Path: "D:/changed", Archived: true})
					}
					return e
				})
				require.NoError(t, db.Callback().Query().Remove("workdir_mutation_failure"))
				require.NoError(t, db.Callback().Create().Remove("workdir_mutation_failure"))
				require.NoError(t, db.Callback().Update().Remove("workdir_mutation_failure"))
				if failAt == 0 {
					require.NoError(t, err)
					require.NotZero(t, out.ID)
					return count
				}
				require.ErrorIs(t, err, injected)
				after, e := q.WithContext(ctx).Order(q.ID).Find()
				require.NoError(t, e)
				require.Equal(t, before, after)
				var afterEffects int64
				require.NoError(t, db.Table("test_fleet_effects").Count(&afterEffects).Error)
				require.Equal(t, effects, afterEffects)
				return count
			}
			total := 0
			t.Run(command+"/"+kind+"/success", func(t *testing.T) { total = exercise(t, 0) })
			for at := 1; at <= total; at++ {
				t.Run(fmt.Sprintf("%s/%s/failure_%d", command, kind, at), func(t *testing.T) { require.GreaterOrEqual(t, exercise(t, at), at) })
			}
		}
	}
}

func TestWorkdirCurrentSourceFailuresPreserveRegisteredIdentity(t *testing.T) {
	for _, fault := range []string{"missing_repository", "missing_workdir", "missing_machine", "revoked", "no_transaction", "invalid_project", "invalid_repository", "invalid_path"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := workdirLifecycleFixture(t)
			tenant, machine, project, repository := guid.ID(7), guid.ID(1), guid.ID(10), guid.ID(12)
			path := "D:/other"
			switch fault {
			case "missing_repository":
				m.repository = nil
			case "missing_workdir":
				row.ID = 999
			case "missing_machine":
				machine = 999
			case "revoked":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			case "invalid_project":
				project = 0
			case "invalid_repository":
				repository = 0
			case "invalid_path":
				path = "relative"
			}
			var err error
			if fault == "no_transaction" {
				_, err = m.RegisterWorkdirOn(ctx, db, tenant, machine, project, repository, path)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error {
					if fault == "missing_workdir" {
						_, e := m.changeWorkdirOn(ctx, tx, tenant, machine, row.ID, 1, WorkdirChange{Path: path})
						return e
					}
					_, e := m.RegisterWorkdirOn(ctx, tx, tenant, machine, project, repository, path)
					return e
				})
			}
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Table(m.names.Workdirs.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestWorkdirHTTPUnavailableReceiptAndCurrentStorageDoNotChangePath(t *testing.T) {
	for _, fault := range []string{"version", "writer", "receipt", "storage", "revoked"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, row, _ := workdirLifecycleFixture(t)
			in := &workdirChangeInput{MachineInput: MachineInput{MachineID: "1"}, WorkdirID: row.ID.String(), IdempotencyKey: "workspace-change", IfMatch: "1", Body: WorkdirChange{Path: "D:/changed"}}
			switch fault {
			case "version":
				in.IfMatch = "bad"
			case "writer":
				m.runtime = nil
			case "storage":
				actual, e := m.writer()
				require.NoError(t, e)
				require.NoError(t, actual.Callback().Query().Before("gorm:query").Register("current_workspace_failure", func(tx *gorm.DB) { tx.AddError(errors.New("workspace unavailable")) }))
				defer actual.Callback().Query().Remove("current_workspace_failure")
			case "revoked":
				require.NoError(t, db.Table(m.names.Machines.String()).Where("id=?", 1).Updates(map[string]any{"state": "REVOKED", "name_key": nil}).Error)
			}
			out, e := m.changeWorkdirHTTP(ctx, in)
			require.Error(t, e)
			require.Nil(t, out)
			q := query.BindWorkdirs(db, m.names)
			saved, e := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, e)
			require.Equal(t, row, workdirView(saved))
		})
	}
}
