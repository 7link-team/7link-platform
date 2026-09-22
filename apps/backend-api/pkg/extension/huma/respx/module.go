package respx

import (
	"context"
	"embed"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "respx"

//go:embed locales/*.json
var locales embed.FS

type Module struct {
	runtime *appenv.Runtime
}

func NewModule(rt *appenv.Runtime) *Module { return &Module{runtime: rt} }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{i18n.Name} }

func (*Module) Initialize(context.Context) error {
	return i18n.RegisterFS(Name, locales, "locales")
}

// The guard layer reports these outcomes. They are declared here because the
// response envelope owns the vocabulary a client sees, and a caller that wrote
// the bare code instead would emit an unresolvable key: localization looks up
// "respx.not_found", never "not_found".
const (
	NotFound                  = Name + ".not_found"
	Forbidden                 = Name + ".forbidden"
	Unauthorized              = Name + ".unauthorized"
	InvalidID                 = Name + ".invalid_id"
	AuthorizationUnavailable  = Name + ".authorization_unavailable"
	AuthenticationUnavailable = Name + ".authentication_unavailable"
	InactiveTenantMembership  = Name + ".inactive_tenant_membership"
	CSRFRejected              = Name + ".csrf_rejected"
)

func messageKey(local string) string {
	return Name + "." + local
}

var (
	_ module.Depender    = (*Module)(nil)
	_ module.Initializer = (*Module)(nil)
)
