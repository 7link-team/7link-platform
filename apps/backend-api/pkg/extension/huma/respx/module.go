package respx

import (
	"context"
	"embed"

	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const namespace = "respx"

//go:embed locales/*.json
var locales embed.FS

type Module struct{}

func (*Module) ModuleName() string { return namespace }

func (*Module) ModuleDependencies() []string { return []string{"i18n"} }

func (*Module) Initialize(context.Context) error {
	return i18n.RegisterFS(namespace, locales, "locales")
}

func NewModule() *Module {
	return &Module{}
}

func messageKey(local string) string {
	return namespace + "." + local
}

var (
	_ module.Depender    = (*Module)(nil)
	_ module.Initializer = (*Module)(nil)
)
