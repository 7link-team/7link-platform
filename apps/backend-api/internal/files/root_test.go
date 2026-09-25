package files

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOrganizationRootProvisioningIsIdempotentAndOrganizationOwned(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, appender: h.service.appender,
		references: h.service.references,
	}
	input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
	now := time.Now().UTC()
	var first, second guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.provisionOrganizationRoot(t.Context(), tx, input, now)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = m.provisionOrganizationRoot(t.Context(), tx, input, now.Add(time.Second))
		return err
	}))
	require.Equal(t, first, second)

	row := treeNode(t, h.db, h.tree.prefix, first)
	require.NotNil(t, row.RootScope)
	require.Equal(t, "entity", *row.RootScope)
	require.NotNil(t, row.RootOwnerID)
	require.Equal(t, guid.ID(80), *row.RootOwnerID)
	require.True(t, row.ACLStopInheritance)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.NodeACL.WithContext(t.Context()).Where(q.NodeACL.NodeID.Eq(first)).Count()
	require.NoError(t, err)
	require.Zero(t, count, "organization roots must not grant their creator a personal ACL")

	policy := nodeAuthorizationPolicy(map[string]authz.DataConstraint{"file_read": {ResourceIDs: []string{"80"}}})
	authorizer, err := newNodeAuthorizer(h.tree, policy, nodeAuthorizationReferences(true, nil), 90, 80, 70, core.AuthorizationSubjects{PrincipalID: 70, Revision: 1})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return authorizer.access(t.Context(), tx, row.SpaceID, 70, row.ID, "read")
	}))

	events, total, err := h.audit.List(t.Context(), audit.Filter{TenantID: 90, EventType: "file_root_created", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, events, 1)
}

func TestOrganizationRootProvisioningRollsBackWithCallerTransaction(t *testing.T) {
	h := newOperationServiceHarness(t)
	want := errors.New("audit unavailable")
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, references: h.service.references,
		appender: func(context.Context, *gorm.DB, audit.DomainEvent) error { return want },
	}
	before := immediateDatabaseSnapshot(t, h.db, h.tree)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.provisionOrganizationRoot(t.Context(), tx, core.FileRootProvisionInput{
			Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 50,
		}, time.Now())
		return err
	})
	require.ErrorIs(t, err, want)
	require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
}

func TestOrganizationRootContentCheckIncludesRetainedDescendants(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := &Module{
		runtime: h.service.runtime, ids: h.service.ids, appender: h.service.appender,
		references: h.service.references,
	}
	input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
	var rootID guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		rootID, err = m.provisionOrganizationRoot(t.Context(), tx, input, time.Now())
		return err
	}))
	check := func() bool {
		var retained bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			retained, err = m.organizationRootHasContent(t.Context(), tx, input)
			return err
		}))
		return retained
	}
	require.False(t, check())
	root := treeNode(t, h.db, h.tree.prefix, rootID)
	_, err := h.tree.createDirectory(t.Context(), h.db, root.SpaceID, 9997, rootID, "Retained", time.Now())
	require.NoError(t, err)
	require.True(t, check())
	_, err = m.organizationRootHasContent(t.Context(), h.db, input)
	require.ErrorIs(t, err, ErrAccountTransaction)
}

// fileOwnerModule builds a Module wired for owner resolution and provisions one
// organization root, returning that root's node ID.
func fileOwnerModule(t *testing.T, h operationServiceHarness, entity core.EntityOwnerResolver, dept core.DepartmentOwnerResolver) *Module {
	t.Helper()
	return &Module{
		runtime: h.service.runtime, ids: h.service.ids, appender: h.service.appender,
		references: h.service.references, entityOwner: entity, deptOwner: dept,
	}
}

func provisionRoot(t *testing.T, h operationServiceHarness, m *Module, input core.FileRootProvisionInput) guid.ID {
	t.Helper()
	var id guid.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.provisionOrganizationRoot(t.Context(), tx, input, time.Now().UTC())
		return err
	}))
	return id
}

func staticOwner(owner authz.ResourceOwner) core.EntityOwnerResolver {
	return func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) { return owner, true, nil }
}

func TestResolveFileOwnerRefusesAnUnusableModule(t *testing.T) {
	h := newOperationServiceHarness(t)
	resolver := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, resolver, core.DepartmentOwnerResolver(resolver))

	var nilModule *Module
	for name, call := range map[string]func() (authz.ResourceOwner, bool, error){
		"nil module": func() (authz.ResourceOwner, bool, error) { return nilModule.resolveFileOwner(t.Context(), 1) },
		"zero file":  func() (authz.ResourceOwner, bool, error) { return m.resolveFileOwner(t.Context(), 0) },
		"negative":   func() (authz.ResourceOwner, bool, error) { return m.resolveFileOwner(t.Context(), -1) },
		"no resolvers": func() (authz.ResourceOwner, bool, error) {
			return fileOwnerModule(t, h, nil, nil).resolveFileOwner(t.Context(), 1)
		},
	} {
		owner, found, err := call()
		assert.ErrorIs(t, err, errOperationUnavailable, name)
		assert.False(t, found, name)
		assert.Equal(t, authz.ResourceOwner{}, owner, name)
	}
}

func TestResolveFileOwnerReportsAbsentFileAsNotFound(t *testing.T) {
	h := newOperationServiceHarness(t)
	resolver := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, resolver, core.DepartmentOwnerResolver(resolver))

	// An unknown ID is "not found", never an error and never a usable owner.
	// The registrar turns this into a 404, so a wrong answer here would either
	// leak existence or reject a real file.
	owner, found, err := m.resolveFileOwner(t.Context(), 999999)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, authz.ResourceOwner{}, owner)
}

func TestResolveFileOwnerReturnsTheRootsOwningScope(t *testing.T) {
	h := newOperationServiceHarness(t)
	entity := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	dept := core.DepartmentOwnerResolver(staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80}))
	m := fileOwnerModule(t, h, entity, dept)

	tenantRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "tenant", TenantID: 90, OwnerID: 90})
	owner, found, err := m.resolveFileOwner(t.Context(), tenantRoot)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, authz.ResourceOwner{TenantID: 90}, owner)

	entityRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80})
	owner, found, err = m.resolveFileOwner(t.Context(), entityRoot)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, authz.ResourceOwner{TenantID: 90, EntityID: 80}, owner)

	deptRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 81})
	owner, found, err = m.resolveFileOwner(t.Context(), deptRoot)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, authz.ResourceOwner{TenantID: 90, EntityID: 80}, owner)
}

func TestResolveFileOwnerRejectsOwnersThatContradictTheSpace(t *testing.T) {
	h := newOperationServiceHarness(t)
	trusted := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(trusted))
	entityRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80})
	deptRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 81})

	// The resolver's answer is cross-checked against the space the file
	// actually lives in. If an entity claimed a different tenant than the
	// file's space, trusting it would hand one tenant authority over another's
	// files, so the mismatch must be refused outright rather than resolved.
	foreignTenant := staticOwner(authz.ResourceOwner{TenantID: 91, EntityID: 80})
	_, found, err := fileOwnerModule(t, h, foreignTenant, core.DepartmentOwnerResolver(foreignTenant)).
		resolveFileOwner(t.Context(), entityRoot)
	assert.ErrorIs(t, err, errFileACL)
	assert.False(t, found)

	// An entity resolver that answers with a different entity than the root it
	// was asked about is equally untrustworthy.
	wrongEntity := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 82})
	_, found, err = fileOwnerModule(t, h, wrongEntity, core.DepartmentOwnerResolver(wrongEntity)).
		resolveFileOwner(t.Context(), entityRoot)
	assert.ErrorIs(t, err, errFileACL)
	assert.False(t, found)

	// A department must resolve to a real entity; a department floating free of
	// one cannot produce an authorization boundary.
	noEntity := staticOwner(authz.ResourceOwner{TenantID: 90})
	_, found, err = fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(noEntity)).
		resolveFileOwner(t.Context(), deptRoot)
	assert.ErrorIs(t, err, errFileACL)
	assert.False(t, found)
}

func TestResolveFileOwnerPropagatesResolverOutcomes(t *testing.T) {
	h := newOperationServiceHarness(t)
	trusted := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(trusted))
	entityRoot := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80})

	// A vanished entity is reported as not found, not as an error, so the
	// caller answers 404 rather than 503.
	absent := core.EntityOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	})
	_, found, err := fileOwnerModule(t, h, absent, core.DepartmentOwnerResolver(trusted)).
		resolveFileOwner(t.Context(), entityRoot)
	require.NoError(t, err)
	assert.False(t, found)

	// A resolver fault must surface so the caller reports a dependency failure
	// instead of silently denying access.
	faulty := core.EntityOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, errOperationUnavailable
	})
	_, found, err = fileOwnerModule(t, h, faulty, core.DepartmentOwnerResolver(trusted)).
		resolveFileOwner(t.Context(), entityRoot)
	assert.ErrorIs(t, err, errOperationUnavailable)
	assert.False(t, found)
}

func TestOrganizationRootProvisioningRejectsInconsistentScopes(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := fileOwnerModule(t, h, nil, nil)
	now := time.Now().UTC()

	// Scope, tenant, owner and entity have to describe one coherent place in
	// the organization. A root provisioned from a contradictory description
	// would be owned by nobody the authorizer can name.
	for name, input := range map[string]core.FileRootProvisionInput{
		"no tenant":            {Scope: "entity", TenantID: 0, OwnerID: 80},
		"no owner":             {Scope: "entity", TenantID: 90, OwnerID: 0},
		"unknown scope":        {Scope: "workspace", TenantID: 90, OwnerID: 80},
		"tenant owned by else": {Scope: "tenant", TenantID: 90, OwnerID: 91},
		"tenant with entity":   {Scope: "tenant", TenantID: 90, OwnerID: 90, EntityID: 80},
		"entity with entity":   {Scope: "entity", TenantID: 90, OwnerID: 80, EntityID: 80},
		"department no entity": {Scope: "department", TenantID: 90, OwnerID: 81},
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			id, err := m.provisionOrganizationRoot(t.Context(), tx, input, now)
			assert.ErrorIs(t, err, errOperationInput, name)
			assert.Zero(t, id, name)
			return nil
		}))
	}

	// Provisioning writes a space, a root and its ACL, so it must be handed a
	// transaction rather than a bare connection.
	_, err := m.provisionOrganizationRoot(t.Context(), h.db, core.FileRootProvisionInput{
		Scope: "entity", TenantID: 90, OwnerID: 80,
	}, now)
	assert.ErrorIs(t, err, ErrAccountTransaction)

	var nilModule *Module
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := nilModule.provisionOrganizationRoot(t.Context(), tx, core.FileRootProvisionInput{
			Scope: "entity", TenantID: 90, OwnerID: 80,
		}, now)
		assert.ErrorIs(t, err, errOperationUnavailable)
		return nil
	}))
}

func TestOrganizationRootHasContentReportsOnlyRealDescendants(t *testing.T) {
	h := newOperationServiceHarness(t)
	m := fileOwnerModule(t, h, nil, nil)
	input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
	rootID := provisionRoot(t, h, m, input)

	// A freshly provisioned root holds nothing. The root's own closure row must
	// not be mistaken for content, otherwise an empty root could never be
	// reclaimed.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		has, err := m.organizationRootHasContent(t.Context(), tx, input)
		require.NoError(t, err)
		assert.False(t, has)
		return nil
	}))

	// A root that was never provisioned simply has no content; that is not an
	// error, because the caller is asking whether it is safe to proceed.
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		has, err := m.organizationRootHasContent(t.Context(), tx, core.FileRootProvisionInput{
			Scope: "entity", TenantID: 90, OwnerID: 88,
		})
		require.NoError(t, err)
		assert.False(t, has)
		return nil
	}))

	tree := directoryTree{prefix: h.tree.prefix}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := tree.createNodeOn(t.Context(), tx, spaceOfNode(t, h.db, h.tree.prefix, rootID), 55501, rootID, "directory", "reports", time.Now().UTC())
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		has, err := m.organizationRootHasContent(t.Context(), tx, input)
		require.NoError(t, err)
		assert.True(t, has, "a root with a child directory is not empty")
		return nil
	}))

	for name, bad := range map[string]core.FileRootProvisionInput{
		"no tenant":            {Scope: "entity", OwnerID: 80},
		"no owner":             {Scope: "entity", TenantID: 90},
		"tenant scope":         {Scope: "tenant", TenantID: 90, OwnerID: 90},
		"entity with entity":   {Scope: "entity", TenantID: 90, OwnerID: 80, EntityID: 80},
		"department no entity": {Scope: "department", TenantID: 90, OwnerID: 81},
	} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, err := m.organizationRootHasContent(t.Context(), tx, bad)
			assert.ErrorIs(t, err, errOperationInput, name)
			return nil
		}))
	}

	_, err := m.organizationRootHasContent(t.Context(), h.db, input)
	assert.ErrorIs(t, err, ErrAccountTransaction)
}

func spaceOfNode(t *testing.T, db *gorm.DB, prefix string, nodeID guid.ID) guid.ID {
	t.Helper()
	q := query.Bind(db, prefix)
	row, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(nodeID)).Take()
	require.NoError(t, err)
	return row.SpaceID
}

func TestResolveFileOwnerHandlesAccountAndSystemRoots(t *testing.T) {
	h := newOperationServiceHarness(t)
	trusted := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(trusted))

	// A person-owned root lives in that person's own space. It used to be able
	// to sit in a tenant's space as a "personal" root, and resolving it there
	// returned the person as the owner; there is no such root now, so the only
	// account case is the one below.
	require.NoError(t, h.tree.createSpace(t.Context(), h.db, 100, 101, 71, "account", time.Now()))
	bindRootFixture(t, h.db, h.tree, 101, rootBinding{scope: "account", ownerID: 71})
	owner, found, err := m.resolveFileOwner(t.Context(), 101)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, authz.ResourceOwner{PrincipalID: 71}, owner)

	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.tree.createSystemSpaceOn(t.Context(), tx, 200, 201, time.Now())
	}))
	owner, found, err = m.resolveFileOwner(t.Context(), 201)
	require.ErrorIs(t, err, errFileACL)
	require.False(t, found)
	require.Equal(t, authz.ResourceOwner{}, owner)
}

func TestResolveFileOwnerPropagatesDepartmentResolverOutcomes(t *testing.T) {
	h := newOperationServiceHarness(t)
	trusted := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
	m := fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(trusted))
	rootID := provisionRoot(t, h, m, core.FileRootProvisionInput{
		Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 81,
	})

	absent := core.DepartmentOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, nil
	})
	owner, found, err := fileOwnerModule(t, h, trusted, absent).resolveFileOwner(t.Context(), rootID)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, authz.ResourceOwner{}, owner)

	faulty := core.DepartmentOwnerResolver(func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{}, false, errOperationUnavailable
	})
	owner, found, err = fileOwnerModule(t, h, trusted, faulty).resolveFileOwner(t.Context(), rootID)
	require.ErrorIs(t, err, errOperationUnavailable)
	require.False(t, found)
	require.Equal(t, authz.ResourceOwner{}, owner)
}

func TestResolveFileOwnerPropagatesEveryReachedStatementFailure(t *testing.T) {
	for _, testCase := range []struct {
		name, table string
		skip        int64
	}{
		{name: "node lookup", table: "file_nodes"},
		{name: "space lookup", table: "file_spaces"},
		{name: "owner space lookup", table: "file_spaces", skip: 1},
		{name: "owner path lookup", table: "file_nodes", skip: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newOperationServiceHarness(t)
			trusted := staticOwner(authz.ResourceOwner{TenantID: 90, EntityID: 80})
			m := fileOwnerModule(t, h, trusted, core.DepartmentOwnerResolver(trusted))
			rootID := provisionRoot(t, h, m, core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80})
			fault := failTableAfter(t, h.service.runtime.Database().Write(), testCase.table, testCase.skip)
			owner, found, err := m.resolveFileOwner(t.Context(), rootID)
			if !fault.Fired() {
				t.Skip("target statement was not reached")
			}
			require.ErrorIs(t, err, errInjectedTableFault)
			require.False(t, found)
			require.Equal(t, authz.ResourceOwner{}, owner)
		})
	}
}

type rootIDSource struct {
	id  int64
	err error
}

func (source rootIDSource) Next() (int64, error) { return source.id, source.err }

func TestNextFileIDRejectsGeneratorErrorsAndNonpositiveIDs(t *testing.T) {
	generated, err := (&Module{ids: rootIDSource{id: 42}}).nextFileID()
	require.NoError(t, err)
	require.Equal(t, guid.ID(42), generated)

	for name, source := range map[string]rootIDSource{
		"zero":     {},
		"negative": {id: -1},
		"error":    {err: errOperationUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			generated, err := (&Module{ids: source}).nextFileID()
			require.ErrorIs(t, err, errOperationInput)
			require.Zero(t, generated)
			if source.err != nil {
				require.ErrorIs(t, err, source.err)
			}
		})
	}
}

func TestOrganizationRootProvisioningFailsClosedOnReferenceErrors(t *testing.T) {
	h := newOperationServiceHarness(t)
	input := core.FileRootProvisionInput{Scope: "department", TenantID: 90, EntityID: 80, OwnerID: 81}
	before := immediateDatabaseSnapshot(t, h.db, h.tree)

	for name, references := range map[string]core.AuthorizationReferencesOn{
		"invalid": func(context.Context, *gorm.DB, guid.ID, core.AuthorizationReferences) (bool, error) {
			return false, nil
		},
		"unavailable": func(context.Context, *gorm.DB, guid.ID, core.AuthorizationReferences) (bool, error) {
			return false, errOperationUnavailable
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := fileOwnerModule(t, h, nil, nil)
			m.references = references
			var rootID guid.ID
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				rootID, err = m.provisionOrganizationRoot(t.Context(), tx, input, time.Now())
				return err
			})
			if name == "invalid" {
				require.ErrorIs(t, err, errOperationForbidden)
			} else {
				require.ErrorIs(t, err, errOperationUnavailable)
			}
			require.Zero(t, rootID)
			require.Equal(t, before, immediateDatabaseSnapshot(t, h.db, h.tree))
		})
	}
}

func TestOrganizationRootOperationsPropagateEveryReachedStatementFailure(t *testing.T) {
	for _, tableRange := range []struct {
		table string
		max   int64
	}{
		{table: "file_spaces", max: 8},
		{table: "file_nodes", max: 7},
		{table: "file_node_closure", max: 2},
	} {
		for skip := int64(0); skip < tableRange.max; skip++ {
			name := fmt.Sprintf("provision/%s/skip%d", tableRange.table, skip)
			t.Run(name, func(t *testing.T) {
				h := newOperationServiceHarness(t)
				m := fileOwnerModule(t, h, nil, nil)
				fault := failTableAfter(t, h.db, tableRange.table, skip)
				var rootID guid.ID
				err := h.db.Transaction(func(tx *gorm.DB) error {
					var err error
					rootID, err = m.provisionOrganizationRoot(t.Context(), tx, core.FileRootProvisionInput{
						Scope: "entity", TenantID: 90, OwnerID: 82,
					}, time.Now())
					return err
				})
				if !fault.Fired() {
					t.Skip("target statement was not reached")
				}
				require.ErrorIs(t, err, errInjectedTableFault)
				require.Zero(t, rootID)
			})
		}
	}

	for _, testCase := range []struct {
		name, table string
		skip        int64
	}{
		{name: "content space lookup", table: "file_spaces"},
		{name: "content space lock", table: "file_spaces", skip: 1},
		{name: "content locked space read", table: "file_spaces", skip: 2},
		{name: "content root lookup", table: "file_nodes"},
		{name: "content closure lookup", table: "file_node_closure"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newOperationServiceHarness(t)
			m := fileOwnerModule(t, h, nil, nil)
			input := core.FileRootProvisionInput{Scope: "entity", TenantID: 90, OwnerID: 80}
			provisionRoot(t, h, m, input)
			fault := failTableAfter(t, h.db, testCase.table, testCase.skip)
			var hasContent bool
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				hasContent, err = m.organizationRootHasContent(t.Context(), tx, input)
				return err
			})
			if !fault.Fired() {
				t.Skip("target statement was not reached")
			}
			require.ErrorIs(t, err, errInjectedTableFault)
			require.False(t, hasContent)
		})
	}
}
