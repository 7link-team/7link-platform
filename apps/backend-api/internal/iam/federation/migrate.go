// Package federation owns external identity providers, identity links, and SAML data.
package federation

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "federation"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

// MigrationSource is this module's migration stream. It is a package function
// so principal erasure can read the stream's version while the module is
// disabled, without holding a Module.
func MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_federation"}
}

func (*Module) MigrationSource() goosex.Source { return MigrationSource() }

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 2},
		{
			Name:    "constraints",
			Version: 3,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "identity", Name: "base"},
			},
		},
		{Name: "replay", Version: 4},
		{Name: "principal-erasure", Version: 5,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "identity-link-transactions", Version: 6},
		// Binding trust quarantines legacy identity links and records the
		// evidence that repaired them; rolling it back would discard that
		// judgement, so it is declared irreversible rather than only refusing
		// once its down section is reached.
		{Name: "binding-trust", Version: 7, Irreversible: true},
		// Social providers only widen this module's own provider_type check, so
		// the milestone depends on nothing outside federation.
		{Name: "social-providers", Version: 8},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
