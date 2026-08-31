package i18n

import (
	"context"

	"7link.com/platform/pkg/appenv"
)

const Name = "i18n"

// Module initializes the empty application translation registry before owning
// modules register their namespaced locale bundles.
type Module struct {
	runtime  *appenv.Runtime
	fallback string
}

func NewModule(rt *appenv.Runtime, fallback string) *Module {
	return &Module{runtime: rt, fallback: fallback}
}

func (*Module) ModuleName() string { return Name }

func (m *Module) Initialize(context.Context) error {
	defaultI18n = New(m.fallback)
	return nil
}
