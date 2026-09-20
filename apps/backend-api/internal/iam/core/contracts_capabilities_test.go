package core_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFrozenIAMCapabilitiesHaveCanonicalOwners locks the exact registry key
// types and their owners. module.Services keys capabilities by reflect.Type.
func TestFrozenIAMCapabilitiesHaveCanonicalOwners(t *testing.T) {
	t.Parallel()

	capabilities := []struct {
		name  string
		typ   reflect.Type
		owner string
	}{
		{"guid.Source", reflect.TypeFor[guid.Source](), "7link.com/platform/internal/infra/guid"},
		{"audit.Appender", reflect.TypeFor[audit.Appender](), "7link.com/platform/internal/iam/audit"},
		{"authz.Registrar", reflect.TypeFor[*authz.Registrar](), "7link.com/platform/pkg/extension/authz"},
		{"core.AuthorizationEvaluator", reflect.TypeFor[core.AuthorizationEvaluator](), "7link.com/platform/internal/iam/core"},
		{"core.EntityConstraintOn", reflect.TypeFor[core.EntityConstraintOn](), "7link.com/platform/internal/iam/core"},
		{"core.TenantConstraintOn", reflect.TypeFor[core.TenantConstraintOn](), "7link.com/platform/internal/iam/core"},
		{"core.AuthorizationSubjectsOn", reflect.TypeFor[core.AuthorizationSubjectsOn](), "7link.com/platform/internal/iam/core"},
		{"core.AuthorizationReferencesOn", reflect.TypeFor[core.AuthorizationReferencesOn](), "7link.com/platform/internal/iam/core"},
		{"authz.PermissionChecker", reflect.TypeFor[authz.PermissionChecker](), "7link.com/platform/pkg/extension/authz"},
		{"core.ActiveMemberChecker", reflect.TypeFor[core.ActiveMemberChecker](), "7link.com/platform/internal/iam/core"},
		{"core.MemberDepartmentReader", reflect.TypeFor[core.MemberDepartmentReader](), "7link.com/platform/internal/iam/core"},
		{"core.MemberTenantResolver", reflect.TypeFor[core.MemberTenantResolver](), "7link.com/platform/internal/iam/core"},
		{"core.DirectoryAuthorizationSourceConnector", reflect.TypeFor[core.DirectoryAuthorizationSourceConnector](), "7link.com/platform/internal/iam/core"},
		// The two table-name connectors are keys of the same registry, so they
		// belong in the same frozen inventory: a second module publishing table
		// names under a colliding key would silently replace core's.
		{"core.DirectoryTableNamesConnector", reflect.TypeFor[core.DirectoryTableNamesConnector](), "7link.com/platform/internal/iam/core"},
		{"core.PrincipalTableNamesConnector", reflect.TypeFor[core.PrincipalTableNamesConnector](), "7link.com/platform/internal/iam/core"},
		{"core.AdministratorGuard", reflect.TypeFor[core.AdministratorGuard](), "7link.com/platform/internal/iam/core"},
		{"authn.InvitationCredentials", reflect.TypeFor[authn.InvitationCredentials](), "7link.com/platform/internal/iam/authn"},
		{"identity.InvitationPrincipalCreator", reflect.TypeFor[identity.InvitationPrincipalCreator](), "7link.com/platform/internal/iam/identity"},
		{"core.RoleGrantStore", reflect.TypeFor[core.RoleGrantStore](), "7link.com/platform/internal/iam/core"},
		{"identity.IdentityProvisioner", reflect.TypeFor[identity.IdentityProvisioner](), "7link.com/platform/internal/iam/identity"},
		{"identity.ServiceAccountValidator", reflect.TypeFor[identity.ServiceAccountValidator](), "7link.com/platform/internal/iam/identity"},
		{"organization.GroupProvisioner", reflect.TypeFor[organization.GroupProvisioner](), "7link.com/platform/internal/iam/organization"},
		{"authn.WebService", reflect.TypeFor[authn.WebService](), "7link.com/platform/internal/iam/authn"},
		// Three separate rewrap keys rather than one shared interface: the
		// registry is keyed by type, so a single name would let whichever module
		// registered last silently replace the others, and the rotation report
		// would come back missing a module without saying anything was missing.
		{"authn.SecretRewrapper", reflect.TypeFor[authn.SecretRewrapper](), "7link.com/platform/internal/iam/authn"},
		{"federation.SecretRewrapper", reflect.TypeFor[federation.SecretRewrapper](), "7link.com/platform/internal/iam/federation"},
		{"provisioning.SecretRewrapper", reflect.TypeFor[provisioning.SecretRewrapper](), "7link.com/platform/internal/iam/provisioning"},
	}

	require.Len(t, capabilities, 26)
	seen := make(map[reflect.Type]struct{}, len(capabilities))
	for _, capability := range capabilities {
		require.NotNil(t, capability.typ)
		_, duplicate := seen[capability.typ]
		assert.False(t, duplicate, "duplicate capability type %s", capability.name)
		seen[capability.typ] = struct{}{}
		assert.Equal(t, capability.owner, ownerPackage(capability.typ), capability.name)
	}

	storeType := reflect.TypeFor[core.RoleGrantStore]()
	assert.Equal(t, 7, storeType.NumField())
	assert.Equal(t, 0, storeType.NumMethod())
	assert.Equal(t, 0, reflect.PointerTo(storeType).NumMethod())
}

func TestIAMRootExportedInterfacesMatchFrozenSet(t *testing.T) {
	t.Parallel()

	declarations := readIAMRootDeclarations(t)
	var actual []string
	for name, declaration := range declarations {
		if declaration.kind == "interface" {
			actual = append(actual, name)
		}
	}
	sort.Strings(actual)
	expected := []string{
		"authn.InvitationCredentials",
		"authn.SecretRewrapper",
		"authn.WebService",
		"core.ActiveMemberChecker",
		"core.AdministratorGuard",
		"core.AuthorizationEvaluator",
		"core.MemberDepartmentReader",
		"federation.SecretRewrapper",
		"identity.IdentityProvisioner",
		"organization.GroupProvisioner",
		"provisioning.SecretRewrapper",
	}
	assert.Equal(t, expected, actual)
}

func TestIAMCapabilitiesAreDeclaredByTheirProviderRoots(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		"audit.Appender":                             "func",
		"authn.InvitationCredentials":                "interface",
		"authn.SecretRewrapper":                      "interface",
		"federation.SecretRewrapper":                 "interface",
		"provisioning.SecretRewrapper":               "interface",
		"authn.WebService":                           "interface",
		"core.ActiveMemberChecker":                   "interface",
		"core.MemberDepartmentReader":                "interface",
		"core.MemberTenantResolver":                  "func",
		"core.AdministratorGuard":                    "interface",
		"core.AuthorizationEvaluator":                "interface",
		"core.EntityConstraintOn":                    "func",
		"core.TenantConstraintOn":                    "func",
		"core.AuthorizationSubjectsOn":               "func",
		"core.AuthorizationReferencesOn":             "func",
		"core.DirectoryAuthorizationSourceConnector": "struct",
		"core.RoleGrantStore":                        "struct",
		"identity.IdentityProvisioner":               "interface",
		"identity.InvitationPrincipalCreator":        "func",
		"identity.ServiceAccountValidator":           "func",
		"organization.GroupProvisioner":              "interface",
	}
	declarations := readIAMRootDeclarations(t)
	for name, kind := range expected {
		declaration, ok := declarations[name]
		require.True(t, ok, "%s must be declared in its provider root", name)
		assert.False(t, declaration.alias, "%s must not be a type alias", name)
		assert.Equal(t, kind, declaration.kind, name)
	}
}

type rootDeclaration struct {
	kind  string
	alias bool
}

func readIAMRootDeclarations(t *testing.T) map[string]rootDeclaration {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	iamRoot := filepath.Dir(filepath.Dir(filename))
	entries, err := os.ReadDir(iamRoot)
	require.NoError(t, err)

	declarations := make(map[string]rootDeclaration)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		packages, parseErr := parser.ParseDir(token.NewFileSet(), filepath.Join(iamRoot, entry.Name()), func(info os.FileInfo) bool {
			return !strings.HasSuffix(info.Name(), "_test.go")
		}, 0)
		require.NoError(t, parseErr)
		for _, pkg := range packages {
			for _, file := range pkg.Files {
				for _, declaration := range file.Decls {
					general, isGeneral := declaration.(*ast.GenDecl)
					if !isGeneral || general.Tok != token.TYPE {
						continue
					}
					for _, specification := range general.Specs {
						typeSpec := specification.(*ast.TypeSpec)
						if !ast.IsExported(typeSpec.Name.Name) {
							continue
						}
						name := entry.Name() + "." + typeSpec.Name.Name
						declarations[name] = rootDeclaration{
							kind:  rootTypeKind(typeSpec.Type),
							alias: typeSpec.Assign.IsValid(),
						}
					}
				}
			}
		}
	}
	return declarations
}

func rootTypeKind(expression ast.Expr) string {
	switch expression.(type) {
	case *ast.FuncType:
		return "func"
	case *ast.InterfaceType:
		return "interface"
	case *ast.StructType:
		return "struct"
	default:
		return "other"
	}
}

func ownerPackage(typ reflect.Type) string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ.PkgPath()
}

type permissionCheckerFixture interface {
	authz.PermissionChecker
	authz.EntityPermissionChecker
}

var _ permissionCheckerFixture = (*completePermissionChecker)(nil)

type completePermissionChecker struct{}

func (*completePermissionChecker) Check(_ context.Context, _ guid.ID, _ string, _ guid.ID) (bool, error) {
	return false, nil
}

func (*completePermissionChecker) CheckPlatform(_ context.Context, _ string, _ guid.ID) (bool, error) {
	return false, nil
}

func (*completePermissionChecker) CheckEntity(_ context.Context, _, _ guid.ID, _ string, _ guid.ID) (bool, error) {
	return false, nil
}

func TestPermissionCheckerConsumerUsesEveryMethod(t *testing.T) {
	t.Parallel()

	checker := permissionCheckerFixture(&completePermissionChecker{})
	_, _ = checker.Check(context.Background(), 0, "", 0)
	_, _ = checker.CheckPlatform(context.Background(), "", 0)
	_, _ = checker.CheckEntity(context.Background(), 0, 0, "", 0)
}
