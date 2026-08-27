package i18n

import "context"

// Module initializes the shared base locale bundle before domain modules
// register their own translations.
type Module struct {
	fallback string
}

func (*Module) ModuleName() string { return "i18n" }

func NewModule(fallback string) *Module {
	return &Module{fallback: fallback}
}

func (m *Module) Initialize(context.Context) error {
	return InitEmbedded(m.fallback)
}
