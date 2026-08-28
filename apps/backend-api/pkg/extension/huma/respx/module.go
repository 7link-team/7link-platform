package respx

import (
	"context"
	"embed"

	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
)

const Name = "respx"

//go:embed locales/*.json
var locales embed.FS

type Module struct{}

func NewModule() *Module { return &Module{} }

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{i18n.Name} }

func (*Module) Initialize(context.Context) error {
	return i18n.RegisterFS(Name, locales, "locales")
}

func messageKey(local string) string {
	return Name + "." + local
}

var (
	_ module.Depender    = (*Module)(nil)
	_ module.Initializer = (*Module)(nil)
)
