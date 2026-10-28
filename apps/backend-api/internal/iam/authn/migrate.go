package authn

//go:generate go run ./dbgen

import (
	"embed"

	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
)

const Name = "authn"

func (*Module) ModuleName() string { return Name }

//go:embed sql
var migrationsFS embed.FS

// MigrationSource is this module's migration stream. It is a package function
// so principal erasure can read the stream's version while the module is
// disabled, without holding a Module.
func MigrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_authn"}
}

func (*Module) MigrationSource() goosex.Source { return MigrationSource() }

func (*Module) MigrationMilestones() []module.MigrationMilestone {
	return []module.MigrationMilestone{
		{Name: "base", Version: 8},
		{
			Name:    "constraints",
			Version: 9,
			Dependencies: []module.MigrationMilestoneRef{
				{Module: "identity", Name: "base"},
			},
		},
		{Name: "indexes", Version: 10},
		{Name: "verification-attempts", Version: 11},
		{Name: "notification-principals", Version: 12},
		{Name: "principal-erasure", Version: 13,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}},
		{Name: "reauthentication", Version: 14},
		{Name: "personal-tokens", Version: 15, Dependencies: []module.MigrationMilestoneRef{{Module: "organization", Name: "base"}}},
		{Name: "account-view-tokens", Version: 16},
		{Name: "access-view-targets", Version: 17},
		{Name: "public-ids", Version: 18},
		// One table of this module's own, with a foreign key to the principals
		// identity already created at its base milestone.
		{Name: "phone-verification", Version: 19,
			Dependencies: []module.MigrationMilestoneRef{{Module: "identity", Name: "base"}}},
		// One column on this module's own challenge table.
		{Name: "mfa-first-factor", Version: 20},
		{Name: "registration-policy", Version: 21},
	}
}

var (
	_ module.MigrationSourceProvider    = (*Module)(nil)
	_ module.MigrationMilestoneProvider = (*Module)(nil)
)
