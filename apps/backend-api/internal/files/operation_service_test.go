package files

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type operationServiceIDs struct{ next atomic.Int64 }

func (ids *operationServiceIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type operationServiceHarness struct {
	service *operationService
	audit   *audit.Service
	db      *gorm.DB
	tree    directoryTree
	input   immediateOperationRequest
	ctx     context.Context
	// ownCtx is the same person with no tenant selected. A person's own file
	// space belongs to no tenant, so anything about it is asked without one --
	// which is what personalContext produces at the route.
	ownCtx context.Context
}

func newOperationServiceHarness(t *testing.T) operationServiceHarness {
	t.Helper()
	db, tree, request := newOperationTree(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&audit.Module{}).MigrationSource()
	source.Prefix = tree.prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, "sqlite", 2))
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: db.Dialector.(*sqlite.Dialector).DSN, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("files-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(tree.prefix))
	ids := &operationServiceIDs{}
	ids.next.Store(10000)
	auditor := audit.NewService(runtime, ids)
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	objects, err := NewAccountObjects(driver, "local", tree.prefix)
	require.NoError(t, err)
	q := query.Bind(db, tree.prefix)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.SpaceID.Eq(guid.ID(1)), q.Node.ID.Eq(guid.ID(2))).UpdateSimple(
		q.Node.RootScope.Value("tenant"), q.Node.RootOwnerID.Value(guid.ID(90)), q.Node.ACLStopInheritance.Value(true),
	)
	require.NoError(t, err)
	aclRows := make([]*model.NodeACL, 0, 13)
	for index, action := range []string{"list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"} {
		aclRows = append(aclRows, &model.NodeACL{
			ID: guid.ID(9000 + index), SpaceID: 1, NodeID: 2, SubjectKind: "principal", SubjectID: 70,
			Effect: "allow", Action: action, Applies: "self_and_descendants", CreatedBy: 70, CreatedAt: time.Now().UnixMilli(),
		})
	}
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(aclRows...))
	service := &operationService{
		runtime: runtime, driver: driver, objects: objects, ids: ids, appender: auditor.DomainAppender(),
		budget: operationBudget{100, 1000}, timeout: 10 * time.Second, uploadTimeout: time.Minute, maxUploadBytes: 1 << 20,
		policy: func(ctx context.Context, tx *gorm.DB, tenant, entity guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
			if tenant != 90 || entity != 80 || actor != 70 {
				return authz.DataConstraint{}, errOperationForbidden
			}
			if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
				return authz.DataConstraint{}, ErrAccountTransaction
			}
			if permission == fileReadAllPermission {
				return authz.DataConstraint{Revision: 1}, ctx.Err()
			}
			return authz.DataConstraint{AllowAll: true, Revision: 1}, ctx.Err()
		},
		tenantPolicy: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
			if tenant != 90 || actor != 70 {
				return authz.DataConstraint{}, errOperationForbidden
			}
			if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
				return authz.DataConstraint{}, ErrAccountTransaction
			}
			return authz.DataConstraint{AllowAll: true, Revision: 1}, ctx.Err()
		},
		subjects: func(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID) (core.AuthorizationSubjects, error) {
			if tenant != 90 || entity != 80 || actor != 70 {
				return core.AuthorizationSubjects{}, errOperationForbidden
			}
			if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
				return core.AuthorizationSubjects{}, ErrAccountTransaction
			}
			return core.AuthorizationSubjects{PrincipalID: actor, Revision: 1}, ctx.Err()
		},
		references: func(ctx context.Context, tx *gorm.DB, tenant guid.ID, _ core.AuthorizationReferences) (bool, error) {
			if tenant != 90 {
				return false, errOperationForbidden
			}
			if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
				return false, ErrAccountTransaction
			}
			return true, ctx.Err()
		},
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70, TenantID: 90, EntityID: 80})
	own := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70})
	return operationServiceHarness{
		service, auditor, db, tree,
		immediateOperationRequest{operationRequest: request}, ctx, own,
	}
}

func (h operationServiceHarness) assertAudit(t *testing.T, count int64) []audit.Event {
	t.Helper()
	return h.assertAuditFor(t, 90, count)
}

// assertAuditFor names the tenant whose record is being read. Work in the
// caller's own space is recorded against no tenant, so asserting it against
// tenant 90 would find nothing and read as "no event was written".
func (h operationServiceHarness) assertAuditFor(t *testing.T, tenantID guid.ID, count int64) []audit.Event {
	t.Helper()
	events, total, err := h.audit.List(t.Context(), audit.Filter{TenantID: tenantID, Limit: 100})
	require.NoError(t, err)
	require.Equal(t, count, total)
	require.Len(t, events, int(count))
	integrity, err := h.audit.Verify(t.Context(), 90)
	require.NoError(t, err)
	require.True(t, integrity.Valid)
	require.Equal(t, count, integrity.VerifiedEvents)
	return events
}

func TestOperationServiceRealAuditAndAuthorizedReplay(t *testing.T) {
	h := newOperationServiceHarness(t)
	h.input.SpaceID = 0
	result, err := h.service.run(h.ctx, "service-copy", h.input)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	events := h.assertAudit(t, 1)
	require.Equal(t, guid.ID(70), events[0].PrincipalID)
	require.Equal(t, "file_copy", events[0].EventType)
	require.Equal(t, "file_operation", events[0].TargetType)
	operation := operationRecord(t, h.db, h.tree, events[0].TargetID)
	require.Equal(t, guid.ID(70), operation.ActorID)
	require.Empty(t, operation.PlanJSON, "synchronous cost is not a persisted plan")
	before := immediateDatabaseSnapshot(t, h.db, h.tree)
	h.service.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == fileViewPermission || permission == filePermissionPrefix+"read" {
			return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
		}
		return authz.DataConstraint{Revision: 1}, nil
	}
	replay, err := h.service.run(h.ctx, "service-copy", h.input)
	require.NoError(t, err, "replay does not require revoked create authority")
	require.Equal(t, result, replay)
	require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
	h.assertAudit(t, 1)
	h.service.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == fileViewPermission {
			return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
		}
		return authz.DataConstraint{Revision: 1}, nil
	}
	replay, err = h.service.run(h.ctx, "service-copy", h.input)
	require.ErrorIs(t, err, errOperationForbidden)
	require.Empty(t, replay)
	h.assertAudit(t, 1)
}

func TestOperationServiceAllowsTenantContext(t *testing.T) {
	h := newOperationServiceHarness(t)
	h.ctx = authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 70, TenantID: 90})

	result, err := h.service.run(h.ctx, "tenant-copy", h.input)
	require.NoError(t, err)
	require.Equal(t, 2, result.Created)
	h.assertAudit(t, 1)
}

func TestOperationServiceHidesForeignDestinationExistence(t *testing.T) {
	h := newOperationServiceHarness(t)
	require.NoError(t, h.db.Exec("UPDATE "+h.tree.prefix+"file_spaces SET tenant_id = 91 WHERE id = 1").Error)
	_, foreignErr := h.service.run(h.ctx, "foreign-destination", h.input)
	require.ErrorIs(t, foreignErr, ErrNotFound)

	h = newOperationServiceHarness(t)
	h.input.DestinationID = 999999
	_, missingErr := h.service.run(h.ctx, "missing-destination", h.input)
	require.ErrorIs(t, missingErr, ErrNotFound)
}

func TestOperationServiceConflictsAndAuditFailuresRollBack(t *testing.T) {
	for _, failure := range []string{"conflict", "audit-insert", "after-audit", "commit"} {
		t.Run(failure, func(t *testing.T) {
			h := newOperationServiceHarness(t)
			want := errors.New("late transaction failure")
			switch failure {
			case "conflict":
				h.input.DirectoryPolicy = "ask"
			case "audit-insert":
				require.NoError(t, h.db.Exec("CREATE TRIGGER fail_service_audit BEFORE INSERT ON "+h.tree.prefix+"iam_audit_events BEGIN SELECT RAISE(ABORT, 'audit insert refused'); END").Error)
			case "after-audit":
				appendEvent := h.service.appender
				h.service.appender = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if err := appendEvent(ctx, tx, event); err != nil {
						return err
					}
					return want
				}
			case "commit":
				require.NoError(t, h.db.Exec("CREATE TABLE service_commit_guard (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES service_commit_guard(id) DEFERRABLE INITIALLY DEFERRED)").Error)
				appendEvent := h.service.appender
				h.service.appender = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if err := appendEvent(ctx, tx, event); err != nil {
						return err
					}
					return tx.Exec("INSERT INTO service_commit_guard (id, parent_id) VALUES (2, 1)").Error
				}
			}
			before := immediateDatabaseSnapshot(t, h.db, h.tree)
			result, err := h.service.run(h.ctx, "rollback", h.input)
			require.Error(t, err)
			require.Empty(t, result)
			switch failure {
			case "conflict":
				immediateFrontier(t, err)
			case "audit-insert":
				require.ErrorContains(t, err, "audit insert refused")
			case "after-audit":
				require.ErrorIs(t, err, want)
			case "commit":
				require.ErrorIs(t, err, gorm.ErrForeignKeyViolated)
			}
			require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
			h.assertAudit(t, 0)
			var heads int64
			require.NoError(t, h.db.Table(h.tree.prefix+"iam_audit_heads").Count(&heads).Error)
			require.Zero(t, heads, "audit head must roll back with tree and result journal")
		})
	}
}

func TestOperationServiceRejectsUntrustedScopeAndMissingDependencies(t *testing.T) {
	for _, failure := range []string{"no-claims", "no-actor", "entity-without-tenant", "foreign-tenant", "account-space", "nil-service", "runtime", "ids", "policy", "subjects", "references", "audit", "timeout", "policy-denied", "entity-denied", "policy-error", "node-denied", "canceled", "deadline"} {
		t.Run(failure, func(t *testing.T) {
			h := newOperationServiceHarness(t)
			ctx := h.ctx
			want := errOperationUnavailable
			switch failure {
			case "no-claims":
				ctx, want = t.Context(), errOperationForbidden
			case "no-actor", "entity-without-tenant":
				claims := &authn.Claims{PrincipalID: 70, TenantID: 90, EntityID: 80}
				if failure == "no-actor" {
					claims.PrincipalID = 0
				}
				if failure == "entity-without-tenant" {
					claims.TenantID = 0
				}
				ctx, want = authn.WithClaims(t.Context(), claims), errOperationForbidden
			case "foreign-tenant":
				require.NoError(t, h.db.Exec("UPDATE "+h.tree.prefix+"file_spaces SET tenant_id = 91 WHERE id = 1").Error)
				want = ErrNotFound
			case "account-space":
				require.NoError(t, h.tree.createSpace(t.Context(), h.db, 100, 101, 70, "account", time.Now()))
				h.input.SpaceID, h.input.DestinationID, h.input.Sources = 100, 101, []guid.ID{101}
				want = ErrNotFound
			case "nil-service":
				h.service = nil
			case "runtime":
				h.service.runtime = nil
			case "ids":
				h.service.ids = nil
			case "policy":
				h.service.policy = nil
			case "subjects":
				h.service.subjects = nil
			case "references":
				h.service.references = nil
			case "audit":
				h.service.appender = nil
			case "timeout":
				h.service.timeout = 0
			case "policy-denied", "entity-denied", "policy-error":
				want = errOperationForbidden
				h.service.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					if failure == "policy-error" {
						return authz.DataConstraint{}, want
					}
					if failure == "entity-denied" {
						return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"80"}, Revision: 1}, nil
					}
					return authz.DataConstraint{Revision: 1}, nil
				}
				h.service.subjects = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.AuthorizationSubjects, error) {
					t.Error("denied IAM scope must fail before node access")
					return core.AuthorizationSubjects{}, nil
				}
			case "node-denied":
				want = errOperationForbidden
				h.service.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
					if permission == fileViewPermission {
						return authz.DataConstraint{AllowAll: true, Revision: 1}, nil
					}
					return authz.DataConstraint{Revision: 1}, nil
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(h.ctx)
				cancel()
				want = context.Canceled
			case "deadline":
				h.service.timeout = 10 * time.Millisecond
				h.service.policy = func(ctx context.Context, _ *gorm.DB, _, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
					<-ctx.Done()
					return authz.DataConstraint{}, ctx.Err()
				}
				want = context.DeadlineExceeded
			}
			before := immediateDatabaseSnapshot(t, h.db, h.tree)
			result, err := h.service.run(ctx, "refusal", h.input)
			require.ErrorIs(t, err, want)
			require.Empty(t, result)
			require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
			h.assertAudit(t, 0)
		})
	}
}

func TestOperationServiceConcurrentReplayAuditsOnce(t *testing.T) {
	h := newOperationServiceHarness(t)
	const workers = 4
	results := make([]operationResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for index := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errs[index] = h.service.run(h.ctx, "concurrent", h.input)
		}()
	}
	wg.Wait()
	for index := range workers {
		require.NoError(t, errs[index])
		require.Equal(t, results[0], results[index])
	}
	h.assertAudit(t, 1)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.Operation.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// TestImmediateOperationSurfacesStorageFailuresAtEveryStage drops one table at
// a time and re-runs the same operation. Each table backs a different stage of
// the run - the tree walk, the permission rules and the operation record - and
// losing any of them has to abort the operation.
// An operation that continued past a missing stage would report success while
// having skipped authorization or left no durable record of what it did.
func TestImmediateOperationSurfacesStorageFailuresAtEveryStage(t *testing.T) {
	// file_operation_items is deliberately absent: this request records an
	// operation row but no item rows, so that table is not on its path and
	// asserting a failure against it would assert something untrue.
	for _, table := range []string{
		"file_node_closure",
		"file_node_acl",
		"file_operations",
	} {
		t.Run(table, func(t *testing.T) {
			h := newOperationServiceHarness(t)
			// Positive control on a clean harness: the same request succeeds,
			// so the failure below is attributable to the dropped table.
			_, err := h.service.run(h.ctx, "control-"+table, h.input)
			require.NoError(t, err)

			require.NoError(t, h.db.Exec("DROP TABLE "+h.tree.prefix+table).Error)
			_, err = h.service.run(h.ctx, "broken-"+table, h.input)
			assert.Error(t, err, "losing %s must abort the operation", table)
		})
	}
}

func TestImmediateOperationRejectsAnUnauthenticatedCaller(t *testing.T) {
	h := newOperationServiceHarness(t)
	// The operation service derives its actor from the request context. With no
	// claims there is nobody to authorize, so it must refuse rather than fall
	// back to an implicit identity.
	_, err := h.service.run(t.Context(), "anonymous", h.input)
	assert.Error(t, err)
}

// tableFault fails database statements against one table once a chosen number
// of them have already gone through.
//
// Dropping a table fails every access to it at once, so only the first guard
// against that table is ever reached and everything behind it stays untested.
// Letting the first few statements succeed moves the failure to the second,
// third or fourth access, which is where most of the untested error handling in
// this package lives: the `if err != nil` after each individual query.
type tableFault struct {
	seen  atomic.Int64
	fired atomic.Bool
	skip  int64
}

// Fired reports whether the injected failure was actually reached. A caller
// that never made that many statements against the table must not be asserted
// against, or the test would claim a guard it never exercised.
func (fault *tableFault) Fired() bool { return fault.fired.Load() }

func (fault *tableFault) trip(tx *gorm.DB, table string) {
	subject := tx.Statement.Table
	if subject == "" {
		subject = tx.Statement.SQL.String()
	}
	if !strings.Contains(subject, table) {
		return
	}
	if fault.seen.Add(1) > fault.skip {
		fault.fired.Store(true)
		_ = tx.AddError(errInjectedTableFault)
	}
}

var errInjectedTableFault = errors.New("files: injected table fault")

// failTableAfter installs the fault and removes it when the test ends.
func failTableAfter(t *testing.T, db *gorm.DB, table string, skip int64) *tableFault {
	t.Helper()
	fault := &tableFault{skip: skip}
	name := fmt.Sprintf("files:fault-%s-%d", table, skip)
	hook := func(tx *gorm.DB) { fault.trip(tx, table) }
	registrations := []struct {
		register func(string, func(*gorm.DB)) error
		remove   func(string) error
	}{
		{db.Callback().Query().Before("gorm:query").Register, db.Callback().Query().Remove},
		{db.Callback().Create().Before("gorm:create").Register, db.Callback().Create().Remove},
		{db.Callback().Update().Before("gorm:update").Register, db.Callback().Update().Remove},
		{db.Callback().Delete().Before("gorm:delete").Register, db.Callback().Delete().Remove},
		{db.Callback().Row().Before("gorm:row").Register, db.Callback().Row().Remove},
	}
	for _, registration := range registrations {
		require.NoError(t, registration.register(name, hook))
	}
	t.Cleanup(func() {
		for _, registration := range registrations {
			require.NoError(t, registration.remove(name))
		}
	})
	return fault
}
