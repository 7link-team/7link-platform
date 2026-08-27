package i18n

import "context"

// Module initializes the empty application translation registry before owning
// modules register their namespaced locale bundles.
type Module struct {
	fallback string
}

func (*Module) ModuleName() string { return "i18n" }

func NewModule(fallback string) *Module {
	return &Module{fallback: fallback}
}

func (m *Module) Initialize(context.Context) error {
	defaultI18n = New(m.fallback)
	return nil
}
