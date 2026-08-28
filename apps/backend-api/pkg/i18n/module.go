package i18n

import "context"

const Name = "i18n"

// Module initializes the empty application translation registry before owning
// modules register their namespaced locale bundles.
type Module struct {
	fallback string
}

func NewModule(fallback string) *Module {
	return &Module{fallback: fallback}
}

func (*Module) ModuleName() string { return Name }

func (m *Module) Initialize(context.Context) error {
	defaultI18n = New(m.fallback)
	return nil
}
