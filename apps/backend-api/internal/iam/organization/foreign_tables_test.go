package organization

import (
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindForeignTables publishes core's set into a repository the way injection
// does, for a harness that builds a service outside the module. The set comes
// from core itself rather than from a list repeated here, so a table core stops
// publishing fails this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bindAccess(core.TableSet(prefix)))
}

// The bound constructors below build a service the way Initialize does: with
// core's published names already in place. A test that used the bare
// constructor would get a service whose every core-owned read fails, which is
// the module's unwired state rather than its running one.
func newBoundService(t *testing.T, runtime *appenv.Runtime, appender audit.Appender, ids guid.Source) *Service {
	t.Helper()
	service := NewService(runtime, appender, ids)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	return service
}

func newBoundGroupService(
	t *testing.T,
	runtime *appenv.Runtime,
	appender audit.Appender,
	members core.ActiveMemberChecker,
	administrators core.AdministratorGuard,
	ids guid.Source,
) *GroupService {
	t.Helper()
	service := NewGroupService(runtime, appender, members, administrators, ids)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	return service
}

func newBoundPositionService(
	t *testing.T,
	runtime *appenv.Runtime,
	appender audit.Appender,
	members core.ActiveMemberChecker,
	administrators core.AdministratorGuard,
	ids guid.Source,
) *PositionService {
	t.Helper()
	service := NewPositionService(runtime, appender, members, administrators, ids)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	return service
}

func newBoundInvitationService(
	t *testing.T,
	runtime *appenv.Runtime,
	appender audit.Appender,
	ids guid.Source,
	credentials InvitationCredentials,
	createPrincipal InvitationPrincipalCreator,
) *InvitationService {
	t.Helper()
	service := NewInvitationService(runtime, appender, ids, credentials, createPrincipal)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	return service
}

func newBoundTenantService(
	t *testing.T,
	runtime *appenv.Runtime,
	appender audit.Appender,
	ids guid.Source,
	bootstrap core.TenantBootstrapper,
	owners identity.TenantOwnerLookup,
) *TenantService {
	t.Helper()
	service := NewTenantService(runtime, appender, ids, bootstrap, owners)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	return service
}

// TestForeignTablesCarryTheOwnersPrefix keeps the resolved name exactly the one
// core published. A name organization invented from its own prefix would send
// a statement to a table the deployment does not have, and the defect would
// appear only where a prefix is configured.
func TestForeignTablesCarryTheOwnersPrefix(t *testing.T) {
	t.Parallel()

	tables := &foreignTables{}
	require.NoError(t, tables.bindAccess(core.TableSet("sl_")))

	members, err := tables.accessTable("iam_tenant_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenant_members", members)
	roles, err := tables.accessTable("iam_roles")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_roles", roles)
}

// TestForeignTablesReportUnboundAsUnavailable is the guarantee the published
// set exists for: a set that never arrived, or one that arrived without the
// name a statement needs, is an explicit error rather than an empty table name.
func TestForeignTablesReportUnboundAsUnavailable(t *testing.T) {
	t.Parallel()

	unbound := &foreignTables{}
	_, err := unbound.accessTable("iam_tenant_members")
	require.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core has not published its table names")

	partial := &foreignTables{}
	require.NoError(t, partial.bindAccess(gormx.NewTableSet(map[string]gormx.TableName{
		"iam_roles": gormx.NewTableName("sl_", "iam_roles"),
	})))
	_, err = partial.accessTable("iam_tenant_members")
	require.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bindAccess(core.TableSet("sl_")), errRepositoryUnavailable)
	_, err = absent.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

// TestStatementsRejectUnpublishedCoreTables walks every statement that reads a
// table core owns, with core's set published complete except for the one name
// that statement needs. Each must report the gap rather than query an empty
// table name.
func TestStatementsRejectUnpublishedCoreTables(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	repository := h.service.repo

	withoutName := func(omit string) gormx.TableSet {
		published := core.TableSet(h.prefix)
		names := make(map[string]gormx.TableName, published.Len())
		for _, logical := range published.Names() {
			if logical == omit {
				continue
			}
			name, _ := published.Lookup(logical)
			names[logical] = name
		}
		return gormx.NewTableSet(names)
	}

	for name, testCase := range map[string]struct {
		omitted string
		call    func() error
	}{
		"dynamic group candidates": {"iam_tenant_members", func() error {
			_, err := repository.listDynamicGroupCandidates(t.Context(), 1, 2)
			return err
		}},
		"group members": {"iam_tenant_members", func() error {
			_, err := repository.listGroupMembers(t.Context(), 1, 2)
			return err
		}},
		"one group member": {"iam_tenant_members", func() error {
			_, err := repository.getGroupMember(t.Context(), 1, 2, 3)
			return err
		}},
		"position members": {"iam_tenant_members", func() error {
			_, err := repository.listPositionMembers(t.Context(), 1, 2)
			return err
		}},
		"one position member": {"iam_tenant_members", func() error {
			_, err := repository.getPositionMember(t.Context(), 1, 2, 3)
			return err
		}},
		"relationship subjects": {"iam_relationships", func() error {
			_, err := repository.countRelationshipSubject(t.Context(), 1, "group", 2)
			return err
		}},
		// The second arm of the same loop: core published the first table and
		// not the second, so a statement that resolved only once would pass.
		"resource relationship subjects": {"iam_resource_relationships", func() error {
			_, err := repository.countRelationshipSubject(t.Context(), 1, "group", 2)
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, repository.foreign.bindAccess(withoutName(testCase.omitted)))
			t.Cleanup(func() { bindForeignTables(t, repository, h.prefix) })

			err := testCase.call()
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "core did not publish "+testCase.omitted)
		})
	}
}
