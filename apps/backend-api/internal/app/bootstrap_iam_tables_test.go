package app

import (
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/internal/todo"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryModulePublishesPrefixedTableNames is the cross-module half of the
// table-name contract. Each module binds the prefix inside its own query
// package, and this is where that promise is read back the way a consumer
// would: through the set the module published, for every name it published.
//
// A module that forgot a table here would be invisible to the modules that
// JOIN it, and one that published an unprefixed name would send its consumers
// to a table the deployment does not have.
func TestEveryModulePublishesPrefixedTableNames(t *testing.T) {
	modules := []struct {
		name     string
		tableSet func(string) gormx.TableSet
	}{
		{"iam/audit", iamaudit.TableSet},
		{"iam/authn", authn.TableSet},
		{"iam/core", core.TableSet},
		{"iam/federation", federation.TableSet},
		{"iam/governance", governance.TableSet},
		{"iam/identity", identity.TableSet},
		{"iam/oauth", oauth.TableSet},
		{"iam/organization", organization.TableSet},
		{"iam/provisioning", provisioning.TableSet},
		{"todo", todo.TableSet},
		{"infra/audit", audit.TableSet},
		{"infra/dlock", dlock.TableSet},
		{"infra/wuid", wuid.TableSet},
	}
	require.Len(t, modules, 13)

	const prefix = "sl_"
	for _, module := range modules {
		t.Run(module.name, func(t *testing.T) {
			plain := module.tableSet("")
			prefixed := module.tableSet(prefix)
			require.Positive(t, prefixed.Len(), "a persistent module publishes at least one table")
			assert.Equal(t, plain.Len(), prefixed.Len(), "the prefix must not change what is published")

			for _, logical := range prefixed.Names() {
				name, ok := prefixed.Lookup(logical)
				require.True(t, ok, logical)
				assert.Equal(t, prefix+logical, name.String(), logical)
				assert.Equal(t, logical, name.Logical(), "the logical name is what errors should say")
				assert.False(t, name.IsZero(), logical)

				// The same table under no prefix differs only by the prefix,
				// which is what makes a half-applied deployment detectable.
				plainName, ok := plain.Lookup(logical)
				require.True(t, ok, logical)
				assert.Equal(t, logical, plainName.String(), logical)
			}
		})
	}
}
