package organization

import (
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindForeignTables publishes core's names into a repository the way injection
// does, for a harness that builds a service outside the module. The names come
// from core's own constructor rather than from a list repeated here, so a table
// core adds or renames reaches this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(core.PublishedAccessTableNames(prefix)))
}

// The bound constructors below build a service the way Initialize does: with
// core's published names already in place. A test that used the bare
// constructor would get a service whose every core-owned read joins an empty
// table name, which is the module's unwired state rather than its running one.
func newBoundService(t *testing.T, runtime *appenv.Runtime, appender audit.Appender, ids guid.Source) *Service {
	t.Helper()
	service := NewService(runtime, appender, ids)
	bindForeignTables(t, service.repo, runtime.TablePrefix())
	// Initialize gives the service the authorizer's answer too, and the
	// department listing refuses without it rather than listing the tenant.
	service.scopes = allDepartmentsConstraint
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

func newBoundInvitationService(
	t *testing.T,
	runtime *appenv.Runtime,
	appender audit.Appender,
	ids guid.Source,
	credentials InvitationCredentials,
	createPrincipal InvitationPrincipalCreator,
) *InvitationService {
	t.Helper()
	service := NewInvitationService(runtime, appender, &grantingAdministratorGuard{}, ids, credentials, createPrincipal)
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

// TestForeignTablesCarryWhatCorePublished pins that the names organization
// reads are core's prefixed names, field for field. A name organization
// invented from its own prefix would send a statement to a table the deployment
// does not have, and the defect would appear only where a prefix is configured.
// Asking for a table core does not have no longer has a runtime failure to
// test: it does not compile.
func TestForeignTablesCarryWhatCorePublished(t *testing.T) {
	t.Parallel()

	tables := &foreignTables{}
	require.NoError(t, tables.bind(core.PublishedAccessTableNames("sl_")))
	require.True(t, tables.bound())

	for want, got := range map[string]string{
		"sl_iam_tenant_members":         tables.access.TenantMembers.String(),
		"sl_iam_roles":                  tables.access.Roles.String(),
		"sl_iam_role_members":           tables.access.RoleMembers.String(),
		"sl_iam_relationships":          tables.access.Relationships.String(),
		"sl_iam_resource_relationships": tables.access.ResourceRelationships.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesAreUnboundUntilCorePublishes pins what the startup check
// reads. A holder that never received core's names, one handed the zero struct,
// and no holder at all are all unbound, so the module refuses to start rather
// than joining an empty table name.
func TestForeignTablesAreUnboundUntilCorePublishes(t *testing.T) {
	t.Parallel()

	var absent *foreignTables
	assert.False(t, absent.bound())
	assert.ErrorIs(t, absent.bind(core.PublishedAccessTableNames("sl_")), errRepositoryUnavailable)

	assert.False(t, (&foreignTables{}).bound())

	unpublished := &foreignTables{}
	require.NoError(t, unpublished.bind(core.AccessTableNames{}))
	assert.False(t, unpublished.bound())
}
